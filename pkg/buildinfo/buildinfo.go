package buildinfo

import "strings"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = ""
)

type Metadata struct {
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
}

func Current() Metadata {
	return Metadata{
		Version:   Version,
		Commit:    Commit,
		BuildDate: Date,
	}
}

func Summary(name string) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(name) != "" {
		parts = append(parts, name)
	}
	parts = append(parts, Version)
	if Commit != "" && Commit != "unknown" {
		parts = append(parts, Commit)
	}
	if Date != "" {
		parts = append(parts, Date)
	}
	return strings.Join(parts, " ")
}
