package server_test

import (
	"net/http"
	"testing"
)

// Departments own Teams (as a child collection). People reference a Department,
// pick a Team name, and carry their own yearly hours + hourly rate for costing.
func TestDepartmentsTeamsAndPeopleCostFields(t *testing.T) {
	token, _ := signup(t, "Dept Team Co")

	// Department with two teams (teams are edited as children).
	dept := do(t, "POST", "/api/v1/docs/departments", token, map[string]any{
		"name":  "Engineering",
		"lines": []map[string]any{{"name": "Platform"}, {"name": "Data"}},
	})
	if dept.Status != http.StatusCreated {
		t.Fatalf("create department failed: %d %s", dept.Status, dept.Raw)
	}
	deptID := dataObj(t, dept)["id"].(string)

	got := do(t, "GET", "/api/v1/docs/departments/"+deptID, token, nil)
	teams, _ := got.Body["lines"].([]any)
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams under department, got %d", len(teams))
	}
	// Teams are filterable by department_id (used by the dependent picker).
	byDept := do(t, "GET", "/api/v1/entities/teams?f_department_id="+deptID, token, nil)
	if tot, _ := byDept.Body["total"].(float64); tot != 2 {
		t.Fatalf("expected 2 teams for department filter, got %v", byDept.Body["total"])
	}

	// Person referencing the department + a team name, with cost inputs.
	person := do(t, "POST", "/api/v1/entities/people", token, map[string]any{
		"full_name":     "Ada Lovelace",
		"department_id": deptID,
		"team":          "Platform",
		"team_leader":   true,
		"worker_type":   "Internal",
		"fte_pct":       100,
		"yearly_hours":  1760,
		"hourly_rate":   90,
	})
	if person.Status != http.StatusCreated {
		t.Fatalf("create person failed: %d %s", person.Status, person.Raw)
	}
	d := dataObj(t, person)
	if d["department_id"] != deptID {
		t.Fatalf("department_id not saved: %v", d["department_id"])
	}
	if d["team"] != "Platform" {
		t.Fatalf("team not saved: %v", d["team"])
	}
	if d["yearly_hours"].(float64) != 1760 || d["hourly_rate"].(float64) != 90 {
		t.Fatalf("cost inputs not saved: hours=%v rate=%v", d["yearly_hours"], d["hourly_rate"])
	}
	// Yearly cost (computed in UI) = 90 * 1760 * 1.0 = 158,400.

	// Catalog checks: departments visible, teams hidden child, people fields.
	meta := do(t, "GET", "/api/v1/meta", token, nil)
	ents, _ := meta.Body["entities"].([]any)
	var deptVisible, teamsHidden, deptRef, teamDependent, hasYearly bool
	for _, e := range ents {
		m := e.(map[string]any)
		switch m["key"] {
		case "departments":
			deptVisible = m["hidden"] != true
		case "teams":
			teamsHidden = m["hidden"] == true
		case "people":
			for _, f := range m["fields"].([]any) {
				fm := f.(map[string]any)
				switch fm["key"] {
				case "department_id":
					deptRef = fm["type"] == "ref" && fm["in_list"] == true
				case "team":
					teamDependent = fm["type"] == "lookup" && fm["filter_by"] == "department_id"
				case "yearly_hours":
					hasYearly = true
				}
			}
		}
	}
	if !deptVisible {
		t.Fatalf("departments should be a visible section")
	}
	if !teamsHidden {
		t.Fatalf("teams should be a hidden child entity")
	}
	if !deptRef {
		t.Fatalf("people.department_id should be a listed ref")
	}
	if !teamDependent {
		t.Fatalf("people.team should be a lookup filtered by department_id")
	}
	if !hasYearly {
		t.Fatalf("people.yearly_hours field missing")
	}
}

// Contracting Company on People points at a Legal Entity. SAR tracking was
// removed from the catalog, so its module must no longer be served.
func TestContractingCompanyIsLegalEntity(t *testing.T) {
	token, _ := signup(t, "Contracting Co")

	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{"legal_name": "Contractor Corp SA"})
	leID := dataObj(t, le)["id"].(string)

	person := do(t, "POST", "/api/v1/entities/people", token, map[string]any{
		"full_name": "Bob Contractor", "worker_type": "Contractor", "contracting_company_id": leID,
	})
	if person.Status != http.StatusCreated {
		t.Fatalf("create person failed: %d %s", person.Status, person.Raw)
	}
	if dataObj(t, person)["contracting_company_id"] != leID {
		t.Fatalf("person contracting_company_id not saved")
	}

	if sar := do(t, "GET", "/api/v1/entities/sar_tracking", token, nil); sar.Status != http.StatusNotFound {
		t.Fatalf("SAR tracking should be gone (404), got %d", sar.Status)
	}

	meta := do(t, "GET", "/api/v1/meta", token, nil)
	ents, _ := meta.Body["entities"].([]any)
	var peopleOK bool
	for _, e := range ents {
		m := e.(map[string]any)
		if m["key"] == "sar_tracking" || m["group"] == "SAR" {
			t.Fatalf("SAR should not be in the catalog")
		}
		for _, f := range m["fields"].([]any) {
			fm := f.(map[string]any)
			if m["key"] == "people" && fm["key"] == "contracting_company_id" && fm["ref"] == "legal_entities" {
				peopleOK = true
			}
		}
	}
	if !peopleOK {
		t.Fatalf("people.contracting_company_id should reference legal_entities")
	}
}
