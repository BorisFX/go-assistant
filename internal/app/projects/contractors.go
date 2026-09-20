package projects

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	contractorsRange = "Подрядчики!A1:G300"
	contractorsSheet = "Подрядчики"
)

// Contractor is one row of the Подрядчики sheet. Group is what a tender is sent
// by: "разошли ТЗ по геодезии" resolves to every contractor in that group.
type Contractor struct {
	Name    string `json:"name"`
	Group   string `json:"group"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Site    string `json:"site,omitempty"`
	Contact string `json:"contact,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Contractors reads the whole sheet. Rows without a name are skipped, the same
// rule the project registry uses.
func (s *Service) Contractors(ctx context.Context) ([]Contractor, error) {
	rows, err := s.sheets.ReadRange(ctx, contractorsRange)
	if err != nil {
		return nil, fmt.Errorf("read contractors: %w", err)
	}

	out := make([]Contractor, 0, len(rows))
	for i, row := range rows {
		if i == 0 || cell(row, 0) == "" {
			continue
		}
		out = append(out, Contractor{
			Name:    cell(row, 0),
			Group:   cell(row, 1),
			Email:   cell(row, 2),
			Phone:   cell(row, 3),
			Site:    cell(row, 4),
			Contact: cell(row, 5),
			Note:    cell(row, 6),
		})
	}
	return out, nil
}

// Groups lists the distinct groups with their sizes, so the assistant can answer
// "кому можем разослать" without dumping the whole sheet.
func (s *Service) Groups(ctx context.Context) (map[string]int, error) {
	list, err := s.Contractors(ctx)
	if err != nil {
		return nil, err
	}
	groups := make(map[string]int)
	for _, c := range list {
		if c.Group != "" {
			groups[c.Group]++
		}
	}
	return groups, nil
}

// ByGroup resolves a spoken group name to its contractors. Matching is by words,
// so "геодезия" finds the "Геодезия, геология, экология" group — the sheet is
// edited by hand and nobody will retype the full heading.
func (s *Service) ByGroup(ctx context.Context, group string) ([]Contractor, error) {
	if strings.TrimSpace(group) == "" {
		return nil, fmt.Errorf("не указана группа подрядчиков")
	}
	list, err := s.Contractors(ctx)
	if err != nil {
		return nil, err
	}

	var matched []Contractor
	for _, c := range list {
		if groupMatches(c.Group, group) {
			matched = append(matched, c)
		}
	}
	if len(matched) == 0 {
		groups, _ := s.Groups(ctx)
		return nil, fmt.Errorf("группа %q не найдена; есть: %s", group, strings.Join(groupNames(groups), ", "))
	}
	return matched, nil
}

// WithEmail keeps only contractors that can actually be written to, and reports
// the rest by name: a silently shorter mailing list is how a contractor is
// dropped from a tender without anyone noticing.
func WithEmail(list []Contractor) (addressable []Contractor, skipped []string) {
	for _, c := range list {
		if strings.Contains(c.Email, "@") {
			addressable = append(addressable, c)
			continue
		}
		skipped = append(skipped, c.Name)
	}
	return addressable, skipped
}

// AddContractor appends a row. The sheet stays the source of truth: the lawyer
// edits it directly, and the bot only adds what it is told to.
func (s *Service) AddContractor(ctx context.Context, c Contractor) error {
	if c.Name == "" || c.Group == "" {
		return fmt.Errorf("подрядчик: название и группа обязательны")
	}
	return s.sheets.AppendRow(ctx, contractorsSheet, []string{
		c.Name, c.Group, c.Email, c.Phone, c.Site, c.Contact, c.Note,
	})
}

func groupMatches(sheetGroup, query string) bool {
	want := textTokens(query)
	if len(want) == 0 {
		return false
	}
	have := textTokens(sheetGroup)
	for _, w := range want {
		if indexOfToken(have, w, 0) < 0 {
			return false
		}
	}
	return true
}

func groupNames(groups map[string]int) []string {
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}
