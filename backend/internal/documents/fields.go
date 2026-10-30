package documents

import "hrcrm/internal/resource"

// TemplateField documents one placeholder a template author can reference.
type TemplateField struct {
	// Placeholder is the exact token to paste into a template, e.g.
	// "{{Legal_Entity_Name}}" or "{{line.description}}".
	Placeholder string `json:"placeholder"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Group names the record a namespaced field reads from (e.g. "Account"),
	// for grouping in the UI. Empty for the original flat placeholders.
	Group string `json:"group,omitempty"`
}

// TemplateFieldCatalog groups the placeholders by where they're used: header
// fields are scalar {{Field}} tokens anywhere in the document; line fields go
// inside the single repeating table row that gets cloned per line item.
type TemplateFieldCatalog struct {
	Header []TemplateField `json:"header"`
	Line   []TemplateField `json:"line"`
	// Schedule fields go inside a table row that is cloned per payment
	// schedule installment (service agreements only).
	Schedule []TemplateField `json:"schedule"`
}

// FieldCatalogFor returns every placeholder the renderer fills: the original
// flat placeholders, then one {{prefix.field}} per field of every entity a
// document can reach (generated from the catalog, so new fields appear
// automatically), and the line / schedule repeating-row fields.
func FieldCatalogFor(reg *resource.Registry) TemplateFieldCatalog {
	c := FieldCatalog()
	c.Header = append(c.Header, TemplateField{"{{Today}}", "Today", "Today's date, formatted dd-mm-yyyy.", ""})
	c.Header = append(c.Header, docFields(reg)...)
	for _, p := range scalarPrefix {
		c.Header = append(c.Header, entityFields(reg, p.Prefix, p.Entity, p.Label, "")...)
	}
	c.Line = rowFields(reg, "line", "invoice_lines", c.Line)
	for entity, prefix := range repeatPrefix {
		c.Schedule = rowFields(reg, prefix, entity, nil)
	}
	return c
}

func entityFields(reg *resource.Registry, prefix, entity, group, note string) []TemplateField {
	e, ok := reg.Get(entity)
	if !ok {
		return nil
	}
	var out []TemplateField
	for _, f := range e.Fields {
		desc := group + " — " + f.Label + fieldHint(f)
		if note != "" {
			desc += ". " + note
		}
		name := prefix + "." + f.Key
		out = append(out, TemplateField{"{{" + name + "}}", name, desc, group})
	}
	return out
}

// docFields lists {{doc.*}}: the union of the source entities' fields, since
// the same template may be generated from a quote or an agreement.
func docFields(reg *resource.Registry) []TemplateField {
	seen := map[string]bool{}
	var out []TemplateField
	for _, key := range []string{"quotes", "invoices"} {
		for _, f := range entityFields(reg, "doc", key, "This document", "Blank when the source document type has no such field") {
			if !seen[f.Name] {
				seen[f.Name] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// rowFields lists a repeating row's fields, keeping any hand-written entries
// in have and appending the rest generated from the child entity.
func rowFields(reg *resource.Registry, prefix, entity string, have []TemplateField) []TemplateField {
	e, ok := reg.Get(entity)
	if !ok {
		return have
	}
	seen := map[string]bool{}
	for _, f := range have {
		seen[f.Name] = true
	}
	for _, f := range e.Fields {
		name := prefix + "." + f.Key
		if f.Type == resource.TypeRef || seen[name] {
			continue // the parent link isn't useful in a row
		}
		have = append(have, TemplateField{"{{" + name + "}}", name, f.Label + fieldHint(f), ""})
	}
	return have
}

func fieldHint(f resource.Field) string {
	switch f.Type {
	case resource.TypeDate, resource.TypeDateTime:
		return " (dd-mm-yyyy)"
	case resource.TypeMoney:
		return " (2 decimals)"
	case resource.TypeBool:
		return " (Yes/No)"
	case resource.TypeRef:
		return " (shown by name)"
	}
	return ""
}

// FieldCatalog returns the original flat placeholders and line fields.
//
// IMPORTANT: keep this in sync with Service.buildValues in service.go — that
// is the map that actually gets substituted, this is its description.
func FieldCatalog() TemplateFieldCatalog {
	return TemplateFieldCatalog{
		Header: []TemplateField{
			{"{{Legal_Entity_Name}}", "Legal_Entity_Name", "Your (selling) legal entity's registered name.", ""},
			{"{{Legal_Entity_Address}}", "Legal_Entity_Address", "Your legal entity's billing address.", ""},
			{"{{Legal_Entity_First_Name}}", "Legal_Entity_First_Name", "Signatory first name, from the seller legal entity.", ""},
			{"{{Legal_Entity_Last_Name}}", "Legal_Entity_Last_Name", "Signatory last name, from the seller legal entity.", ""},
			{"{{Legal_Entity_Title}}", "Legal_Entity_Title", "Signatory title, from the seller legal entity.", ""},
			{"{{Buying_Entity_Name}}", "Buying_Entity_Name", "The customer's legal (billing) entity name, falling back to the account name.", ""},
			{"{{Buying_Entity_Address}}", "Buying_Entity_Address", "The customer's billing address.", ""},
			{"{{Account_Name}}", "Account_Name", "The customer account's name.", ""},
			{"{{Quote_Description}}", "Quote_Description", "The document description (also used in the file name).", ""},
			{"{{Invoice_Date}}", "Invoice_Date", "Document date (agreement / quote date), formatted dd-mm-yyyy.", ""},
			{"{{Due_Date}}", "Due_Date", "Due date (quotes: valid until), formatted dd-mm-yyyy.", ""},
			{"{{Service_Start_Date}}", "Service_Start_Date", "Service period start, formatted dd-mm-yyyy.", ""},
			{"{{Service_End_Date}}", "Service_End_Date", "Service period end, formatted dd-mm-yyyy.", ""},
			{"{{Currency}}", "Currency", "Currency code (e.g. EUR, USD).", ""},
			{"{{Net_Total}}", "Net_Total", "Sum of line nets, to 2 decimals.", ""},
			{"{{VAT_Pct}}", "VAT_Pct", "VAT percentage applied.", ""},
			{"{{VAT_Total}}", "VAT_Total", "VAT amount, to 2 decimals.", ""},
			{"{{Grand_Total}}", "Grand_Total", "Net + VAT, to 2 decimals.", ""},
			{"{{Invoice_ID}}", "Invoice_ID", "The document's unique identifier (raw id).", ""},
			{"{{Account_ID}}", "Account_ID", "The customer account's id (raw). Prefer Account_Name for display.", ""},
			{"{{Legal_Entity}}", "Legal_Entity", "The selling legal entity's id (raw). Prefer Legal_Entity_Name for display.", ""},
		},
		Line: []TemplateField{
			{"{{line.line_no}}", "line.line_no", "Row number of the line item.", ""},
			{"{{line.sku}}", "line.sku", "Product SKU.", ""},
			{"{{line.description}}", "line.description", "Line item description.", ""},
			{"{{line.qty}}", "line.qty", "Quantity.", ""},
			{"{{line.unit_price}}", "line.unit_price", "Unit price, to 2 decimals.", ""},
			{"{{line.line_net}}", "line.line_net", "Line total (qty × unit price), to 2 decimals.", ""},
		},
	}
}
