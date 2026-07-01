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

// Contracting Company on People points at a Legal Entity; on SAR it is a
// read-only reference (auto-filled in the UI from the chosen person).
func TestContractingCompanyIsLegalEntity(t *testing.T) {
	token, _ := signup(t, "Contracting Co")

	le := do(t, "POST", "/api/v1/entities/legal_entities", token, map[string]any{"legal_name": "Contractor Corp SA"})
	leID := dataObj(t, le)["id"].(string)

	// Person's contracting company references the legal entity.
	person := do(t, "POST", "/api/v1/entities/people", token, map[string]any{
		"full_name": "Bob Contractor", "worker_type": "Contractor", "contracting_company_id": leID,
	})
	if person.Status != http.StatusCreated {
		t.Fatalf("create person failed: %d %s", person.Status, person.Raw)
	}
	personID := dataObj(t, person)["id"].(string)
	if dataObj(t, person)["contracting_company_id"] != leID {
		t.Fatalf("person contracting_company_id not saved")
	}

	// SAR stores the same reference (the UI auto-fills it from the person).
	sar := do(t, "POST", "/api/v1/entities/sar_tracking", token, map[string]any{
		"person_id": personID, "contracting_company_id": leID,
	})
	if sar.Status != http.StatusCreated {
		t.Fatalf("create SAR failed: %d %s", sar.Status, sar.Raw)
	}
	if dataObj(t, sar)["contracting_company_id"] != leID {
		t.Fatalf("SAR contracting_company_id not saved")
	}

	// Catalog: both point at legal_entities; SAR's is read-only. On SAR, the
	// rate is read-only (auto-filled from the person) and internal_validation
	// is a boolean checkbox.
	meta := do(t, "GET", "/api/v1/meta", token, nil)
	ents, _ := meta.Body["entities"].([]any)
	var peopleOK, sarOK, rateRO, amountRO, validationBool, personContractorOnly bool
	for _, e := range ents {
		m := e.(map[string]any)
		for _, f := range m["fields"].([]any) {
			fm := f.(map[string]any)
			if m["key"] == "people" && fm["key"] == "contracting_company_id" && fm["ref"] == "legal_entities" {
				peopleOK = true
			}
			if m["key"] == "sar_tracking" {
				switch fm["key"] {
				case "person_id":
					if fm["option_filter_field"] == "worker_type" && fm["option_filter_value"] == "Contractor" {
						personContractorOnly = true
					}
				case "contracting_company_id":
					if fm["ref"] == "legal_entities" && fm["read_only"] == true {
						sarOK = true
					}
				case "rate":
					if fm["read_only"] == true {
						rateRO = true
					}
				case "amount":
					if fm["read_only"] == true {
						amountRO = true
					}
				case "internal_validation":
					if fm["type"] == "bool" {
						validationBool = true
					}
				}
			}
		}
	}
	if !peopleOK {
		t.Fatalf("people.contracting_company_id should reference legal_entities")
	}
	if !sarOK {
		t.Fatalf("sar contracting_company_id should be a read-only legal_entities ref")
	}
	if !rateRO {
		t.Fatalf("sar rate should be read-only")
	}
	if !amountRO {
		t.Fatalf("sar amount should be read-only")
	}
	if !validationBool {
		t.Fatalf("sar internal_validation should be a bool field")
	}
	if !personContractorOnly {
		t.Fatalf("sar person_id should be filtered to Contractor worker_type")
	}
}
