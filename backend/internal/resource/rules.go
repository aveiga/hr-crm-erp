package resource

import (
	"context"

	"github.com/google/uuid"
)

// Field rules (defaults, mirrored fields, conditional disabling) and per-entity
// write hooks. They run inside Repo writes, so every path — the REST handlers,
// MCP tools and conversions — gets the same behavior; DocService applies the
// field rules to document headers too.

// WriteHooks are optional per-entity callbacks around generic Repo writes.
// Before receives the merged row (stored values overlaid with the input; just
// the input on create) and returns extra values to write, or an error to
// reject the write. After receives the row before and after the write (nil
// before on create, nil after on delete).
type WriteHooks struct {
	Before func(ctx context.Context, tenantID uuid.UUID, row map[string]any) (map[string]any, error)
	After  func(ctx context.Context, tenantID uuid.UUID, before, after map[string]any)
}

// WithRegistry enables field rules (they resolve the refs they mirror).
func (r *Repo) WithRegistry(reg *Registry) *Repo {
	r.reg = reg
	return r
}

// Hook registers write hooks for an entity.
func (r *Repo) Hook(entityKey string, h WriteHooks) {
	if r.hooks == nil {
		r.hooks = map[string]WriteHooks{}
	}
	r.hooks[entityKey] = h
}

// rowLoader fetches one tenant row of an entity by id; nil when missing.
type rowLoader func(entityKey string, id any) map[string]any

func (r *Repo) poolLoader(ctx context.Context, tenantID uuid.UUID) rowLoader {
	return func(entityKey string, id any) map[string]any {
		e, ok := r.reg.Get(entityKey)
		if !ok {
			return nil
		}
		uid, err := uuid.Parse(asStr(id))
		if err != nil {
			return nil
		}
		row, err := r.Get(ctx, e, tenantID, uid)
		if err != nil {
			return nil
		}
		return row
	}
}

// ruleValues computes the values field rules force on a (merged) row:
// mirrored fields copy from the referenced row while their ref is set, and
// conditionally-disabled fields are cleared.
func ruleValues(e Entity, row map[string]any, load rowLoader) map[string]any {
	out := map[string]any{}
	targets := map[string]map[string]any{}
	for _, f := range e.Fields {
		if f.FillFrom != "" && asStr(row[f.FillFrom]) != "" {
			ref, ok := e.field(f.FillFrom)
			if !ok {
				continue
			}
			src, seen := targets[f.FillFrom]
			if !seen {
				src = load(ref.RefKey, row[f.FillFrom])
				targets[f.FillFrom] = src
			}
			if src != nil { // a dangling ref leaves the field as it is
				out[f.Key] = src[f.FillField]
			}
		}
		if f.DisabledWhen != "" && asStr(row[f.DisabledWhen]) == f.DisabledValue {
			out[f.Key] = nil
		}
	}
	return out
}

// applyDefaults fills Default values for fields the input leaves empty.
func applyDefaults(e Entity, input map[string]any) {
	for _, f := range e.Fields {
		if f.Default != "" && isEmpty(input[f.Key]) {
			input[f.Key] = f.Default
		}
	}
}

// prepareWrite applies defaults (create), field rules and the Before hook to
// an input, returning the input to write. stored is nil on create.
func (r *Repo) prepareWrite(ctx context.Context, e Entity, tenantID uuid.UUID, stored, input map[string]any) (map[string]any, error) {
	if r.reg == nil {
		return input, nil
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	if stored == nil {
		applyDefaults(e, out)
	}
	merged := map[string]any{}
	for k, v := range stored {
		merged[k] = v
	}
	for k, v := range out {
		merged[k] = v
	}
	for k, v := range ruleValues(e, merged, r.poolLoader(ctx, tenantID)) {
		out[k] = v
		merged[k] = v
	}
	if h, ok := r.hooks[e.Key]; ok && h.Before != nil {
		extra, err := h.Before(ctx, tenantID, merged)
		if err != nil {
			return nil, err
		}
		for k, v := range extra {
			out[k] = v
		}
	}
	return out, nil
}

func (r *Repo) afterWrite(ctx context.Context, e Entity, tenantID uuid.UUID, before, after map[string]any) {
	if h, ok := r.hooks[e.Key]; ok && h.After != nil {
		h.After(ctx, tenantID, before, after)
	}
}

func (r *Repo) hooked(e Entity) bool {
	_, ok := r.hooks[e.Key]
	return ok
}
