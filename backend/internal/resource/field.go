// Package resource implements a metadata-driven entity engine. Every business
// module (people, accounts, invoices, ...) is described once as an Entity with
// typed Fields. From that single description we generate the database schema,
// generic tenant-scoped CRUD, and a catalog the frontend uses to render tables
// and forms. Adding a module is a data change, not new code.
package resource

import "fmt"

type FieldType string

const (
	TypeText     FieldType = "text"
	TypeLongText FieldType = "longtext"
	TypeInt      FieldType = "int"
	TypeNumber   FieldType = "number"
	TypeMoney    FieldType = "money"
	TypePercent  FieldType = "percent"
	TypeDate     FieldType = "date"
	TypeDateTime FieldType = "datetime"
	TypeBool     FieldType = "bool"
	TypeSelect   FieldType = "select"
	TypeRef      FieldType = "ref"
	TypeEmail    FieldType = "email"
	// TypeLookup stores a chosen value from another entity (e.g. a product's
	// SKU). Unlike TypeRef (which stores the target's id), it stores the value
	// of ValueField and displays LabelField.
	TypeLookup FieldType = "lookup"
)

// Field describes one column of an entity.
type Field struct {
	Key        string    `json:"key"`
	Label      string    `json:"label"`
	Type       FieldType `json:"type"`
	Required   bool      `json:"required"`
	Options    []string  `json:"options,omitempty"`     // for select
	RefKey     string    `json:"ref,omitempty"`         // ref/lookup: target entity key
	RefLabel   string    `json:"ref_label,omitempty"`   // ref: target field used as display label
	ValueField string    `json:"value_field,omitempty"` // lookup: target field whose value is stored
	LabelField string    `json:"label_field,omitempty"` // lookup: target field shown to the user
	// Dependent options: only show ref/lookup options where the target's
	// FilterField equals this record's FilterBy sibling field value (e.g. team
	// options filtered by the chosen department). Disabled until the sibling set.
	FilterBy    string `json:"filter_by,omitempty"`
	FilterField string `json:"filter_field,omitempty"`
	// Static option filter: restrict a ref/lookup's selectable options to target
	// rows whose OptionFilterField equals OptionFilterValue (e.g. SAR person_id
	// shows only people whose worker_type = "Contractor"). Unlike FilterBy this
	// is a constant, not a sibling-dependent value.
	OptionFilterField string `json:"option_filter_field,omitempty"`
	OptionFilterValue string `json:"option_filter_value,omitempty"`
	// Mirrored field: when the sibling ref FillFrom is set, this field's value
	// is copied from the referenced row's FillField and is read-only (e.g. a
	// person's country from their location). With FillFrom empty it's a normal
	// editable field. Enforced server-side on every write (see rules.go).
	FillFrom  string `json:"fill_from,omitempty"`
	FillField string `json:"fill_field,omitempty"`
	// Conditionally disabled: when sibling DisabledWhen equals DisabledValue the
	// field is disabled in the UI and cleared on save (e.g. an internal
	// person has no contracting company).
	DisabledWhen  string `json:"disabled_when,omitempty"`
	DisabledValue string `json:"disabled_value,omitempty"`
	// Default is applied on create when no value is given (and pre-filled in
	// the UI's new-record form).
	Default  string `json:"default,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"` // computed/auto-filled; not user-editable
	InList   bool   `json:"in_list"`             // show in the table view
	Help     string `json:"help,omitempty"`
}

// Child links a parent document to a sub-collection (e.g. quotes -> quote_lines).
type Child struct {
	Entity string `json:"entity"` // child entity key, e.g. "quote_lines"
	FK     string `json:"fk"`     // child field linking to the parent, e.g. "quote_id"
	Label  string `json:"label"`  // UI label, e.g. "Line Items"
}

// View is a saved list filter offered on an entity's list page, e.g. "Overdue".
// Query uses the list endpoint's filter params (f_, fin_, fmin_, fmax_), whose
// values may use date tokens (@today, @today-1, @month) and @empty.
type View struct {
	Label string `json:"label"`
	Query string `json:"query"`
}

// Entity describes a business module.
type Entity struct {
	Key        string  `json:"key"`         // url + table suffix, e.g. "invoices"
	Label      string  `json:"label"`       // singular, e.g. "Invoice"
	Plural     string  `json:"plural"`      // e.g. "Invoices"
	Group      string  `json:"group"`       // nav grouping, e.g. "CRM"
	TitleField string  `json:"title_field"` // field used as the record title
	Icon       string  `json:"icon"`
	Hidden     bool    `json:"hidden"`    // not shown in nav (managed via its parent)
	ReadOnly   bool    `json:"read_only"` // shown in nav as a searchable list, but not writable here
	Children   []Child `json:"children,omitempty"`
	Views      []View  `json:"views,omitempty"`
	// Columns lists the fields shown in the list view, in order. Empty means
	// the first in-list fields plus the money totals.
	Columns []string `json:"columns,omitempty"`
	Fields  []Field  `json:"fields"`
}

func (e Entity) Table() string { return "ent_" + e.Key }

func (e Entity) field(key string) (Field, bool) {
	for _, f := range e.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

func (f Field) sqlType() string {
	switch f.Type {
	case TypeInt:
		return "BIGINT"
	case TypeNumber, TypeMoney, TypePercent:
		return "DOUBLE PRECISION"
	case TypeDate:
		return "DATE"
	case TypeDateTime:
		return "TIMESTAMPTZ"
	case TypeBool:
		return "BOOLEAN"
	default:
		return "TEXT"
	}
}

func (f Field) defaultClause() string {
	if f.Type == TypeBool {
		return " DEFAULT false"
	}
	return ""
}

// Registry holds all entities, addressable by key.
type Registry struct {
	order []string
	byKey map[string]Entity
}

func NewRegistry(entities ...Entity) (*Registry, error) {
	r := &Registry{byKey: map[string]Entity{}}
	for _, e := range entities {
		if _, dup := r.byKey[e.Key]; dup {
			return nil, fmt.Errorf("duplicate entity key %q", e.Key)
		}
		r.byKey[e.Key] = e
		r.order = append(r.order, e.Key)
	}
	return r, nil
}

func (r *Registry) Get(key string) (Entity, bool) {
	e, ok := r.byKey[key]
	return e, ok
}

func (r *Registry) All() []Entity {
	out := make([]Entity, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.byKey[k])
	}
	return out
}

// Groups returns the distinct section names, in first-seen (catalog) order.
// These are the columns of the per-user permissions matrix.
func (r *Registry) Groups() []string {
	seen := map[string]bool{}
	var out []string
	for _, k := range r.order {
		g := r.byKey[k].Group
		if g != "" && !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	return out
}

// --- small constructors to keep the catalog readable -----------------------

func txt(key, label string) Field  { return Field{Key: key, Label: label, Type: TypeText, InList: true} }
func long(key, label string) Field { return Field{Key: key, Label: label, Type: TypeLongText} }
func email(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeEmail, InList: true}
}
func num(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeNumber, InList: true}
}
func money(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeMoney, InList: true}
}
func pct(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypePercent, InList: true}
}
func intf(key, label string) Field { return Field{Key: key, Label: label, Type: TypeInt, InList: true} }
func date(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeDate, InList: true}
}
func datetime(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeDateTime, InList: true}
}
func boolf(key, label string) Field {
	return Field{Key: key, Label: label, Type: TypeBool, InList: true}
}
func sel(key, label string, opts ...string) Field {
	return Field{Key: key, Label: label, Type: TypeSelect, Options: opts, InList: true}
}
func ref(key, label, target, refLabel string) Field {
	return Field{Key: key, Label: label, Type: TypeRef, RefKey: target, RefLabel: refLabel, InList: true}
}
func lookup(key, label, source, valueField, labelField string) Field {
	return Field{Key: key, Label: label, Type: TypeLookup, RefKey: source,
		ValueField: valueField, LabelField: labelField, InList: true}
}

func req(f Field) Field                   { f.Required = true; return f }
func withHelp(f Field, help string) Field { f.Help = help; return f }
func hidden(f Field) Field                { f.InList = false; return f }
func ro(f Field) Field                    { f.ReadOnly = true; return f }

// filteredBy makes a ref/lookup depend on a sibling field: options are limited
// to target rows whose targetField equals this record's siblingKey value.
func filteredBy(f Field, siblingKey, targetField string) Field {
	f.FilterBy = siblingKey
	f.FilterField = targetField
	return f
}

// filledFrom mirrors a field from the row a sibling ref points at (read-only
// while the ref is set).
func filledFrom(f Field, siblingRef, sourceField string) Field {
	f.FillFrom = siblingRef
	f.FillField = sourceField
	return f
}

// disabledWhen disables and clears a field while a sibling equals value.
func disabledWhen(f Field, sibling, value string) Field {
	f.DisabledWhen = sibling
	f.DisabledValue = value
	return f
}

func withDefault(f Field, v string) Field { f.Default = v; return f }

// optionWhere restricts a ref/lookup's selectable options to target rows whose
// targetField equals a constant value (e.g. only "Contractor" people).
func optionWhere(f Field, targetField, value string) Field {
	f.OptionFilterField = targetField
	f.OptionFilterValue = value
	return f
}
