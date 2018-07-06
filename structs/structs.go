package structs

type Action struct {
	Creator    string `json:"creator"`
	ZondUuid   string `json:"zond"`
	Action     string `json:"action"`
	Param      string `json:"param"`
	Result     string `json:"result"`
	Uuid       string `json:"uuid"`
	ParentUUID string `json:"parent"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	Target     string `json:"target"`
	Repeat     string `json:"repeat"`
}

type Result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Zond struct {
	Creator string `json:"creator"`
	Uuid    string `json:"uuid"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
}

type Channels struct {
	Action    string   `json:"action"`
	Zonds     []string `json:"zonds"`
	Countries []string `json:"countries"`
	Cities    []string `json:"cities"`
	ASNs      []string `json:"asns"`
}

type ErrorMessage struct {
	Text  string
	Color string
}

// Geodata struct
type Geodata struct {
	City                         string  `json:"city"`
	Country                      string  `json:"country"`
	CountryCode                  string  `json:"country_code"`
	Longitude                    float64 `json:"lon"`
	Latitude                     float64 `json:"lat"`
	AutonomousSystemNumber       uint    `json:"asn"`
	AutonomousSystemOrganization string  `json:"provider"`
}
