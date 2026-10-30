package azure

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// An Azure agreement's Notes carry the actual usage of the account's
// subscriptions over the agreement period, per subscription and quarter. The
// block sits between these markers and is rewritten on every refresh/true-up;
// text outside it is the user's and is kept.
const (
	notesStart = "--- Azure usage by subscription (updated automatically) ---"
	notesEnd   = "--- end of Azure usage ---"
)

// usageNotes renders the actual (synced) usage and fees of the agreement's
// subscriptions over its period, up to the synced-through date.
func (s *Service) usageNotes(ctx context.Context, tenantID uuid.UUID, a *agreementCtx) (string, error) {
	start, end := a.quarters[0].Start, a.quarters[len(a.quarters)-1].End
	var b strings.Builder
	b.WriteString(notesStart + "\n")
	done := func() string { return b.String() + notesEnd }

	through, err := parseDay(a.through)
	switch {
	case len(a.subs) == 0:
		b.WriteString("No Azure subscriptions are mapped to this account.\n")
		return done(), nil
	case err != nil || through.Before(start):
		b.WriteString(fmt.Sprintf("No actual usage synced for %s – %s yet.\n", dmy(start), dmy(end)))
		return done(), nil
	}
	actualEnd := end
	if through.Before(end) {
		actualEnd = through
	}
	aggs, err := s.usage(ctx, tenantID, subIDs(a.subs), fmtDay(start), fmtDay(actualEnd))
	if err != nil {
		return "", err
	}

	type amounts struct{ usage, fee float64 }
	bySub := map[string]*amounts{}
	byQuarter := map[string]map[string]*amounts{} // sub -> quarter label -> amounts
	subOf := map[string]subRow{}
	for _, sub := range a.subs {
		subOf[sub.ID] = sub
	}
	total := map[string]*amounts{} // per currency
	cur := ""
	for _, u := range aggs {
		fee := u.Cost * a.rates.pct(subOf[u.Sub], u.Month) / 100
		if bySub[u.Sub] == nil {
			bySub[u.Sub], byQuarter[u.Sub] = &amounts{}, map[string]*amounts{}
		}
		bySub[u.Sub].usage += u.Cost
		bySub[u.Sub].fee += fee
		q := quarterLabel(u.Month)
		if byQuarter[u.Sub][q] == nil {
			byQuarter[u.Sub][q] = &amounts{}
		}
		byQuarter[u.Sub][q].usage += u.Cost
		byQuarter[u.Sub][q].fee += fee
		if total[u.Currency] == nil {
			total[u.Currency] = &amounts{}
		}
		total[u.Currency].usage += u.Cost
		total[u.Currency].fee += fee
		cur = u.Currency
	}

	b.WriteString(fmt.Sprintf("Actual usage %s – %s (synced through %s)\n\n", dmy(start), dmy(actualEnd), dmy(through)))
	for _, sub := range a.subs {
		typ := sub.Type
		if typ == "" {
			typ = "no type"
		}
		amt := bySub[sub.ID]
		if amt == nil || round2(amt.usage) == 0 {
			b.WriteString(fmt.Sprintf("• %s (%s) · %s · no usage in this period\n", sub.Name, sub.SubID, typ))
			continue
		}
		b.WriteString(fmt.Sprintf("• %s (%s) · %s · fee %s%% · usage %s %s · fee %s %s\n", sub.Name, sub.SubID, typ,
			fmtNum(amt.fee/amt.usage*100), fmtMoney(amt.usage), cur, fmtMoney(amt.fee), cur))
		labels := make([]string, 0, len(byQuarter[sub.ID]))
		for q := range byQuarter[sub.ID] {
			labels = append(labels, q)
		}
		sort.Slice(labels, func(i, j int) bool { return quarterKey(labels[i]) < quarterKey(labels[j]) })
		for _, q := range labels {
			qa := byQuarter[sub.ID][q]
			b.WriteString(fmt.Sprintf("    %s: usage %s · fee %s\n", q, fmtMoney(qa.usage), fmtMoney(qa.fee)))
		}
	}
	curs := make([]string, 0, len(total))
	for c := range total {
		curs = append(curs, c)
	}
	sort.Strings(curs)
	for _, c := range curs {
		b.WriteString(fmt.Sprintf("\nTotal: usage %s %s · fee %s %s\n", fmtMoney(total[c].usage), c, fmtMoney(total[c].fee), c))
	}
	if actualEnd.Before(end) {
		b.WriteString(fmt.Sprintf("%s – %s is not in yet (forecast in the quarter lines).\n", dmy(actualEnd.AddDate(0, 0, 1)), dmy(end)))
	}
	return done(), nil
}

// mergeNotes puts the usage block into the notes: replacing an earlier block,
// else after the user's text.
func mergeNotes(notes, block string) string {
	if i := strings.Index(notes, notesStart); i >= 0 {
		if j := strings.Index(notes[i:], notesEnd); j >= 0 {
			return notes[:i] + block + notes[i+j+len(notesEnd):]
		}
	}
	if strings.TrimSpace(notes) == "" {
		return block
	}
	return strings.TrimRight(notes, "\n") + "\n\n" + block
}

// quarterLabel is "Q2 2026" for a YYYY-MM-DD date.
func quarterLabel(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return fmt.Sprintf("Q%d %d", (int(t.Month())-1)/3+1, t.Year())
}

// quarterKey sorts "Q2 2026" chronologically ("2026Q2").
func quarterKey(label string) string {
	if parts := strings.Fields(label); len(parts) == 2 {
		return parts[1] + parts[0]
	}
	return label
}

// fmtMoney formats 12345.6 as "12,345.60".
func fmtMoney(v float64) string {
	s := strconv.FormatFloat(round2(v), 'f', 2, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, dec := s[:len(s)-3], s[len(s)-3:]
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String() + dec
	}
	return b.String() + dec
}

// fmtNum formats a percentage without trailing zeros ("25", "27.5").
func fmtNum(v float64) string { return strconv.FormatFloat(round2(v), 'f', -1, 64) }
