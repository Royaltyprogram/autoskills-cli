package response

type HealthResp struct {
	Reply string `json:"reply"`
}

type ProbeResp struct {
	Status    string `json:"status"`
	Store     string `json:"store,omitempty"`
	Version   string `json:"version,omitempty"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
}
