package documents

// TemplateField documents one placeholder a template author can reference.
type TemplateField struct {
	// Placeholder is the exact token to paste into a template, e.g.
	// "{{Legal_Entity_Name}}" or "{{line.description}}".
	Placeholder string `json:"placeholder"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TemplateFieldCatalog groups the placeholders by where they're used: header
// fields are scalar {{Field}} tokens anywhere in the document; line fields go
// inside the single repeating table row that gets cloned per line item.
type TemplateFieldCatalog struct {
	Header []TemplateField `json:"header"`
	Line   []TemplateField `json:"line"`
}

// FieldCatalog returns every placeholder the renderer fills.
//
// IMPORTANT: keep this in sync with Service.buildValues (header) and
// Service.lines (line) in service.go — those are the maps that actually get
// substituted, this is their human-readable description.
func FieldCatalog() TemplateFieldCatalog {
	return TemplateFieldCatalog{
		Header: []TemplateField{
			{"{{Legal_Entity_Name}}", "Legal_Entity_Name", "Your (selling) legal entity's registered name."},
			{"{{Legal_Entity_Address}}", "Legal_Entity_Address", "Your legal entity's billing address."},
			{"{{Legal_Entity_First_Name}}", "Legal_Entity_First_Name", "Reserved signatory first name — currently rendered blank."},
			{"{{Legal_Entity_Last_Name}}", "Legal_Entity_Last_Name", "Reserved signatory last name — currently rendered blank."},
			{"{{Legal_Entity_Title}}", "Legal_Entity_Title", "Reserved signatory title — currently rendered blank."},
			{"{{Buying_Entity_Name}}", "Buying_Entity_Name", "The customer's legal (billing) entity name, falling back to the account name."},
			{"{{Buying_Entity_Address}}", "Buying_Entity_Address", "The customer's billing address."},
			{"{{Account_Name}}", "Account_Name", "The customer account's name."},
			{"{{Quote_Description}}", "Quote_Description", "The quote/invoice description (also used in the file name)."},
			{"{{Invoice_Date}}", "Invoice_Date", "Invoice date, formatted dd-mm-yyyy."},
			{"{{Due_Date}}", "Due_Date", "Payment due date, formatted dd-mm-yyyy."},
			{"{{Service_Start_Date}}", "Service_Start_Date", "Service period start, formatted dd-mm-yyyy."},
			{"{{Service_End_Date}}", "Service_End_Date", "Service period end, formatted dd-mm-yyyy."},
			{"{{Currency}}", "Currency", "Currency code (e.g. EUR, USD)."},
			{"{{Net_Total}}", "Net_Total", "Sum of line nets, to 2 decimals."},
			{"{{VAT_Pct}}", "VAT_Pct", "VAT percentage applied."},
			{"{{VAT_Total}}", "VAT_Total", "VAT amount, to 2 decimals."},
			{"{{Grand_Total}}", "Grand_Total", "Net + VAT, to 2 decimals."},
			{"{{Invoice_ID}}", "Invoice_ID", "The invoice's unique identifier (raw id)."},
			{"{{Account_ID}}", "Account_ID", "The customer account's id (raw). Prefer Account_Name for display."},
			{"{{Legal_Entity}}", "Legal_Entity", "The selling legal entity's id (raw). Prefer Legal_Entity_Name for display."},
		},
		Line: []TemplateField{
			{"{{line.line_no}}", "line.line_no", "Row number of the line item."},
			{"{{line.sku}}", "line.sku", "Product SKU."},
			{"{{line.description}}", "line.description", "Line item description."},
			{"{{line.qty}}", "line.qty", "Quantity."},
			{"{{line.unit_price}}", "line.unit_price", "Unit price, to 2 decimals."},
			{"{{line.line_net}}", "line.line_net", "Line total (qty × unit price), to 2 decimals."},
		},
	}
}
