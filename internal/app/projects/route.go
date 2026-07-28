// Package projects turns the project registry spreadsheet into answers about
// where each project stands: which stage it is on, which documents the stage
// expects, and which of them are not there yet.
//
// Routes live in the spreadsheet rather than in code on purpose. Processes
// change with legislation, and a lawyer must be able to correct a route without
// a deploy.
package projects

import (
	"fmt"
	"strconv"
	"strings"
)

// Stage is one step of a route.
type Stage struct {
	Num      int      `json:"num"`
	Name     string   `json:"name"`
	Expected []string `json:"expected"`
	Agencies []string `json:"agencies,omitempty"`
	NormDays int      `json:"norm_days,omitempty"`
}

// Folder is the stage's folder name inside the project folder.
func (s Stage) Folder() string {
	return fmt.Sprintf("%02d_%s", s.Num, strings.ReplaceAll(s.Name, " ", "_"))
}

// Route is an ordered set of stages for one kind of service.
type Route struct {
	Code   string  `json:"code"`
	Stages []Stage `json:"stages"`
}

// Folders lists every folder a project on this route should have. The source
// folder is outside the route: documents that start the work belong to no
// single stage, and correspondence spans all of them.
func (r Route) Folders() []string {
	out := make([]string, 0, len(r.Stages)+2)
	out = append(out, SourceFolder)
	for _, s := range r.Stages {
		out = append(out, s.Folder())
	}
	return append(out, MailFolder)
}

const (
	SourceFolder = "00_Исходные"
	MailFolder   = "99_Переписка"
)

// splitList reads a semicolon-separated cell.
func splitList(cell string) []string {
	var out []string
	for _, p := range strings.Split(cell, ";") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func cell(row []string, i int) string {
	if i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// ParseRoutes reads the Маршруты sheet: one row per route-and-stage, header
// first. Malformed rows are skipped rather than failing the whole parse — a
// half-edited spreadsheet must not take the assistant down.
func ParseRoutes(rows [][]string) map[string]Route {
	routes := map[string]Route{}
	for i, row := range rows {
		if i == 0 {
			continue // header
		}
		code := cell(row, 0)
		name := cell(row, 2)
		if code == "" || name == "" {
			continue
		}
		num, err := strconv.Atoi(cell(row, 1))
		if err != nil {
			continue
		}

		r := routes[code]
		r.Code = code
		r.Stages = append(r.Stages, Stage{
			Num:      num,
			Name:     name,
			Expected: splitList(cell(row, 3)),
			Agencies: splitList(cell(row, 4)),
			NormDays: atoiOrZero(cell(row, 5)),
		})
		routes[code] = r
	}
	return routes
}

func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
