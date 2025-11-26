package config

type Redirect struct {
	From      string `toml:"from" json:"from" json5:"from"`
	To        string `toml:"to" json:"to" json5:"to"`
	Permanent bool   `toml:"permanent" json:"permanent" json5:"permanent"`
}

type Header struct {
	Name  string  `toml:"name" json:"name" json5:"name"`
	Value *string `toml:"value" json:"value" json5:"value"`
	On    *string `toml:"on" json:"on" json5:"on"`
}

type Form struct {
	From string `toml:"from" json:"from" json5:"from"`
	To   string `toml:"to" json:"to" json5:"to"`
	Then string `toml:"then" json:"then" json5:"then"`
}

type XmitConfig struct {
	Fallback  string     `toml:"fallback" json:"fallback" json5:"fallback"`
	FourOFour string     `toml:"404" json:"404" json5:"404"`
	Headers   []Header   `toml:"headers" json:"headers" json5:"headers"`
	Redirects []Redirect `toml:"redirects" json:"redirects" json5:"redirects"`
	Forms     []Form     `toml:"forms" json:"forms" json5:"forms"`
}
