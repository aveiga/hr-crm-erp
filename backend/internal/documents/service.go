package documents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"hrcrm/internal/resource"
)

// Service builds Offer and Service Agreement (IGTR) documents from quote,
// sales order or service agreement data, reusing the metadata engine's
// repository so it stays tenant-scoped.
type Service struct {
	reg          *resource.Registry
	repo         *resource.Repo
	templatesDir string
	store        *TemplateStore
}

func NewService(reg *resource.Registry, repo *resource.Repo, templatesDir string, store *TemplateStore) *Service {
	return &Service{reg: reg, repo: repo, templatesDir: templatesDir, store: store}
}

// Sources are the document entities a template can be generated from.
// Generation follows the record's references upward (service agreement →
// quote → opportunity, plus account, contact and seller legal entity) so a
// template can use any field of any of them.
var Sources = map[string]bool{"quotes": true, "invoices": true}

// scalarPrefix is the placeholder namespace of each entity reachable from a
// source document: {{quote.description}}, {{account.billing_email}}, …
var scalarPrefix = []struct{ Entity, Prefix, Label string }{
	{"quotes", "quote", "Quote"},
	{"invoices", "service_agreement", "Service Agreement"},
	{"opportunities", "opportunity", "Opportunity"},
	{"accounts", "account", "Account"},
	{"contacts", "contact", "Contact"},
	{"legal_entities", "legal_entity", "Our Legal Entity (seller)"},
}

// repeatPrefix maps secondary child collections to their repeating-row
// placeholder namespace. The primary line items are always "line".
var repeatPrefix = map[string]string{"payment_schedule": "schedule"}

// Build fills a template from any source document (see Sources). templateID
// optionally selects an uploaded tenant template; empty uses the default.
func (s *Service) Build(ctx context.Context, kind, entityKey string, tenantID, id uuid.UUID, templateID string) ([]byte, string, error) {
	if !Sources[entityKey] {
		return nil, "", fmt.Errorf("documents cannot be generated from %s", entityKey)
	}
	src, ok := s.reg.Get(entityKey)
	if !ok {
		return nil, "", fmt.Errorf("%s entity missing", entityKey)
	}
	row, err := s.repo.Get(ctx, src, tenantID, id)
	if err != nil {
		return nil, "", err
	}

	loaded := s.gather(ctx, tenantID, src, row)
	values := s.buildValues(row, loaded["accounts"], loaded["legal_entities"])
	for k, v := range s.scalarValues(ctx, tenantID, src, row, loaded) {
		values[k] = v
	}
	repeats := s.repeats(ctx, tenantID, src, id)

	body, isDocx, err := s.loadTemplate(ctx, tenantID, kind, templateID)
	if err != nil {
		return nil, "", err
	}

	var docx []byte
	if isDocx {
		docx, err = renderDocxZipRepeats(body, values, repeats)
	} else {
		docx, err = assembleDocx(renderBodyRepeats(string(body), values, repeats))
	}
	if err != nil {
		return nil, "", err
	}

	name := cleanFilename(fmt.Sprintf("%s - %s - %s",
		strings.ToUpper(kind), values["Legal_Entity_Name"], values["Quote_Description"]))
	return docx, name + ".docx", nil
}

// BuildPDFFrom is Build followed by a LibreOffice PDF conversion.
func (s *Service) BuildPDFFrom(ctx context.Context, sofficeBin, kind, entityKey string, tenantID, id uuid.UUID, templateID string) ([]byte, string, error) {
	docx, name, err := s.Build(ctx, kind, entityKey, tenantID, id, templateID)
	if err != nil {
		return nil, "", err
	}
	pdf, err := ConvertToPDF(ctx, sofficeBin, docx)
	if err != nil {
		return nil, "", err
	}
	return pdf, strings.TrimSuffix(name, ".docx") + ".pdf", nil
}

// gather walks references from the source row breadth-first and loads each
// entity in scalarPrefix at most once, keyed by entity. The nearest reference
// wins, so the source's own legal entity beats the account's default.
func (s *Service) gather(ctx context.Context, tenantID uuid.UUID, src resource.Entity, row map[string]any) map[string]map[string]any {
	wanted := map[string]bool{}
	for _, p := range scalarPrefix {
		wanted[p.Entity] = true
	}
	loaded := map[string]map[string]any{src.Key: row}
	queue := []resource.Entity{src}
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		for _, f := range e.Fields {
			if f.Type != resource.TypeRef || !wanted[f.RefKey] || loaded[f.RefKey] != nil {
				continue
			}
			target := s.lookup(ctx, f.RefKey, tenantID, getStr(loaded[e.Key], f.Key))
			if len(target) == 0 {
				continue
			}
			loaded[f.RefKey] = target
			if te, ok := s.reg.Get(f.RefKey); ok {
				queue = append(queue, te)
			}
		}
	}
	return loaded
}

// scalarValues renders every field of every gathered record as
// {{prefix.field}}, the source document itself as {{doc.field}}, and {{Today}}.
func (s *Service) scalarValues(ctx context.Context, tenantID uuid.UUID, src resource.Entity, row map[string]any, loaded map[string]map[string]any) map[string]string {
	label := s.refLabeler(ctx, tenantID)
	out := map[string]string{"Today": time.Now().Format("02-01-2006")}
	for _, p := range scalarPrefix {
		e, ok := s.reg.Get(p.Entity)
		if !ok {
			continue
		}
		rec := loaded[p.Entity]
		for _, f := range e.Fields {
			out[p.Prefix+"."+f.Key] = formatField(f, rec[f.Key], label)
		}
	}
	for _, f := range src.Fields {
		out["doc."+f.Key] = formatField(f, row[f.Key], label)
	}
	return out
}

// repeats loads the source's child collections as repeating-row values:
// "line" for line items, and repeatPrefix names for secondary collections.
func (s *Service) repeats(ctx context.Context, tenantID uuid.UUID, src resource.Entity, id uuid.UUID) map[string][]map[string]string {
	label := s.refLabeler(ctx, tenantID)
	out := map[string][]map[string]string{}
	for i, c := range src.Children {
		prefix := "line"
		if i > 0 {
			prefix = repeatPrefix[c.Entity]
		}
		ce, ok := s.reg.Get(c.Entity)
		if !ok || prefix == "" {
			continue
		}
		res, err := s.repo.List(ctx, ce, tenantID, resource.ListOptions{
			Page: 1, PageSize: 200, Sort: "line_no",
			Filters: map[string]string{c.FK: id.String()},
		})
		if err != nil {
			continue
		}
		rows := make([]map[string]string, 0, len(res.Data))
		for _, r := range res.Data {
			m := map[string]string{}
			for _, f := range ce.Fields {
				m[f.Key] = formatField(f, r[f.Key], label)
			}
			rows = append(rows, m)
		}
		out[prefix] = rows
	}
	return out
}

// refLabeler resolves a ref field's stored id to the target's title, memoized
// for one generation.
func (s *Service) refLabeler(ctx context.Context, tenantID uuid.UUID) func(f resource.Field, id string) string {
	memo := map[string]string{}
	return func(f resource.Field, id string) string {
		k := f.RefKey + "/" + id
		if v, ok := memo[k]; ok {
			return v
		}
		v := ""
		if target, ok := s.reg.Get(f.RefKey); ok {
			v = getStr(s.lookup(ctx, f.RefKey, tenantID, id), target.TitleField)
		}
		memo[k] = v
		return v
	}
}

// formatField renders a stored value for a document: dates dd-mm-yyyy, money
// to 2 decimals, booleans Yes/No, refs as the target's title.
func formatField(f resource.Field, v any, refLabel func(resource.Field, string) string) string {
	if v == nil {
		return ""
	}
	switch f.Type {
	case resource.TypeDate, resource.TypeDateTime:
		return fmtDate(fmt.Sprint(v))
	case resource.TypeMoney:
		return fmtMoney(v)
	case resource.TypeNumber, resource.TypePercent, resource.TypeInt:
		return fmtNum(v)
	case resource.TypeBool:
		if b, ok := v.(bool); ok && b {
			return "Yes"
		}
		return "No"
	case resource.TypeRef:
		return refLabel(f, fmt.Sprint(v))
	default:
		return fmt.Sprint(v)
	}
}

// buildValues fills the original (pre-namespacing) placeholders, which
// uploaded templates in production already use. They read from whichever
// source document is being generated, falling back across the field names
// quotes, orders and agreements use for the same concept.
func (s *Service) buildValues(doc, account, legal map[string]any) map[string]string {
	v := map[string]string{
		"Invoice_ID":              getStr(doc, "id"),
		"Legal_Entity":            getStr(doc, "legal_entity_id"),
		"Account_ID":              getStr(doc, "account_id"),
		"Account_Name":            getStr(account, "account_name"),
		"Buying_Entity_Name":      firstNonEmpty(getStr(account, "legal_entity_name"), getStr(account, "account_name"), getStr(doc, "buyer_name")),
		"Buying_Entity_Address":   firstNonEmpty(getStr(account, "legal_entity_address"), getStr(doc, "buyer_address")),
		"Legal_Entity_Name":       firstNonEmpty(getStr(legal, "legal_name"), getStr(doc, "legal_entity_name")),
		"Legal_Entity_Address":    firstNonEmpty(getStr(legal, "billing_address"), getStr(doc, "legal_entity_billing_address")),
		"Legal_Entity_First_Name": getStr(legal, "signatory_first_name"),
		"Legal_Entity_Last_Name":  getStr(legal, "signatory_last_name"),
		"Legal_Entity_Title":      getStr(legal, "signatory_title"),
		"Invoice_Date":            fmtDate(firstNonEmpty(getStr(doc, "invoice_date"), getStr(doc, "quote_date"))),
		"Due_Date":                fmtDate(firstNonEmpty(getStr(doc, "due_date"), getStr(doc, "valid_until"))),
		"Service_Start_Date":      fmtDate(getStr(doc, "service_start_date")),
		"Service_End_Date":        fmtDate(getStr(doc, "service_end_date")),
		"Currency":                getStr(doc, "currency"),
		"Quote_Description":       firstNonEmpty(getStr(doc, "quote_description"), getStr(doc, "description")),
		"Net_Total":               fmtMoney(doc["net_total"]),
		"VAT_Total":               fmtMoney(doc["vat_total"]),
		"Grand_Total":             fmtMoney(doc["grand_total"]),
		"VAT_Pct":                 fmtNum(doc["vat_pct"]),
	}
	return v
}

func (s *Service) lookup(ctx context.Context, entKey string, tenantID uuid.UUID, idStr string) map[string]any {
	if idStr == "" {
		return map[string]any{}
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return map[string]any{}
	}
	e, ok := s.reg.Get(entKey)
	if !ok {
		return map[string]any{}
	}
	row, err := s.repo.Get(ctx, e, tenantID, id)
	if err != nil {
		return map[string]any{}
	}
	return row
}

// loadTemplate returns the template bytes and whether it's a full .docx.
// Resolution order: an uploaded tenant template selected by templateID, then a
// drop-in .docx/.xml in TEMPLATES_DIR, then the template embedded in the binary
// (so generation works out of the box from any working directory).
func (s *Service) loadTemplate(ctx context.Context, tenantID uuid.UUID, kind, templateID string) ([]byte, bool, error) {
	if templateID != "" && s.store != nil {
		id, err := uuid.Parse(templateID)
		if err != nil {
			return nil, false, fmt.Errorf("invalid template id")
		}
		content, err := s.store.Content(ctx, tenantID, id, kind)
		if err != nil {
			return nil, false, err
		}
		return content, true, nil
	}
	if s.templatesDir != "" {
		docxPath := filepath.Join(s.templatesDir, kind+".docx")
		if b, err := os.ReadFile(docxPath); err == nil {
			return b, true, nil
		}
		xmlPath := filepath.Join(s.templatesDir, kind+".xml")
		if b, err := os.ReadFile(xmlPath); err == nil {
			return b, false, nil
		}
	}
	b, err := builtinTemplates.ReadFile("templates/" + kind + ".xml")
	if err != nil {
		return nil, false, fmt.Errorf("no template for %q (not in TEMPLATES_DIR or embedded)", kind)
	}
	return b, false, nil
}

// --- formatting helpers ----------------------------------------------------

func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func fmtMoney(v any) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case float64:
		return fmt.Sprintf("%.2f", n)
	default:
		return fmt.Sprint(v)
	}
}

func fmtNum(v any) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
		return fmt.Sprintf("%g", n)
	case int64:
		return fmt.Sprintf("%d", n)
	default:
		return fmt.Sprint(v)
	}
}

// fmtDate turns an RFC3339 timestamp into dd-mm-yyyy, matching the VBA output.
func fmtDate(s string) string {
	if len(s) >= 10 {
		y, m, d := s[0:4], s[5:7], s[8:10]
		return fmt.Sprintf("%s-%s-%s", d, m, y)
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func cleanFilename(s string) string {
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	s = repl.Replace(strings.TrimSpace(s))
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if s == "" {
		s = "document"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
