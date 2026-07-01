package documents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"hrcrm/internal/resource"
)

// Service builds Offer and IGTR documents from invoice data, reusing the
// metadata engine's repository so it stays tenant-scoped.
type Service struct {
	reg          *resource.Registry
	repo         *resource.Repo
	templatesDir string
	store        *TemplateStore
}

func NewService(reg *resource.Registry, repo *resource.Repo, templatesDir string, store *TemplateStore) *Service {
	return &Service{reg: reg, repo: repo, templatesDir: templatesDir, store: store}
}

// BuildDocx returns the filled .docx bytes plus a suggested filename for the
// given document kind ("offer" or "igtr") sourced from an invoice. templateID
// optionally selects an uploaded tenant template; empty uses the default.
func (s *Service) BuildDocx(ctx context.Context, kind string, tenantID, invoiceID uuid.UUID, templateID string) ([]byte, string, error) {
	invEntity, ok := s.reg.Get("invoices")
	if !ok {
		return nil, "", fmt.Errorf("invoices entity missing")
	}
	inv, err := s.repo.Get(ctx, invEntity, tenantID, invoiceID)
	if err != nil {
		return nil, "", err
	}

	account := s.lookup(ctx, "accounts", tenantID, getStr(inv, "account_id"))
	legal := s.lookup(ctx, "legal_entities", tenantID, getStr(inv, "legal_entity_id"))
	lines := s.lines(ctx, tenantID, invoiceID)

	values := s.buildValues(inv, account, legal)

	body, isDocx, err := s.loadTemplate(ctx, tenantID, kind, templateID)
	if err != nil {
		return nil, "", err
	}

	var docx []byte
	if isDocx {
		docx, err = renderDocxZip(body, values, lines)
	} else {
		rendered := renderBody(string(body), values, lines)
		docx, err = assembleDocx(rendered)
	}
	if err != nil {
		return nil, "", err
	}

	name := cleanFilename(fmt.Sprintf("%s - %s - %s",
		strings.ToUpper(kind), values["Legal_Entity_Name"], values["Quote_Description"]))
	return docx, name + ".docx", nil
}

// BuildPDF builds the docx then converts it to PDF via LibreOffice.
func (s *Service) BuildPDF(ctx context.Context, sofficeBin, kind string, tenantID, invoiceID uuid.UUID, templateID string) ([]byte, string, error) {
	docx, name, err := s.BuildDocx(ctx, kind, tenantID, invoiceID, templateID)
	if err != nil {
		return nil, "", err
	}
	pdf, err := ConvertToPDF(ctx, sofficeBin, docx)
	if err != nil {
		return nil, "", err
	}
	return pdf, strings.TrimSuffix(name, ".docx") + ".pdf", nil
}

func (s *Service) buildValues(inv, account, legal map[string]any) map[string]string {
	v := map[string]string{
		"Invoice_ID":              getStr(inv, "id"),
		"Legal_Entity":            getStr(inv, "legal_entity_id"),
		"Account_ID":              getStr(inv, "account_id"),
		"Account_Name":            getStr(account, "account_name"),
		"Buying_Entity_Name":      firstNonEmpty(getStr(account, "legal_entity_name"), getStr(account, "account_name")),
		"Buying_Entity_Address":   getStr(account, "legal_entity_address"),
		"Legal_Entity_Name":       firstNonEmpty(getStr(legal, "legal_name"), getStr(inv, "legal_entity_name")),
		"Legal_Entity_Address":    firstNonEmpty(getStr(legal, "billing_address"), getStr(inv, "legal_entity_billing_address")),
		"Legal_Entity_First_Name": "",
		"Legal_Entity_Last_Name":  "",
		"Legal_Entity_Title":      "",
		"Invoice_Date":            fmtDate(getStr(inv, "invoice_date")),
		"Due_Date":                fmtDate(getStr(inv, "due_date")),
		"Service_Start_Date":      fmtDate(getStr(inv, "service_start_date")),
		"Service_End_Date":        fmtDate(getStr(inv, "service_end_date")),
		"Currency":                getStr(inv, "currency"),
		"Quote_Description":       getStr(inv, "quote_description"),
		"Net_Total":               fmtMoney(inv["net_total"]),
		"VAT_Total":               fmtMoney(inv["vat_total"]),
		"Grand_Total":             fmtMoney(inv["grand_total"]),
		"VAT_Pct":                 fmtNum(inv["vat_pct"]),
	}
	return v
}

func (s *Service) lines(ctx context.Context, tenantID, invoiceID uuid.UUID) []map[string]string {
	lineEntity, ok := s.reg.Get("invoice_lines")
	if !ok {
		return nil
	}
	res, err := s.repo.List(ctx, lineEntity, tenantID, resource.ListOptions{
		Page: 1, PageSize: 200, Sort: "line_no",
		Filters: map[string]string{"invoice_id": invoiceID.String()},
	})
	if err != nil {
		return nil
	}
	out := make([]map[string]string, 0, len(res.Data))
	for _, row := range res.Data {
		out = append(out, map[string]string{
			"line_no":     fmtNum(row["line_no"]),
			"sku":         getStr(row, "sku"),
			"description": getStr(row, "description"),
			"qty":         fmtNum(row["qty"]),
			"unit_price":  fmtMoney(row["unit_price"]),
			"line_net":    fmtMoney(row["line_net"]),
		})
	}
	return out
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
