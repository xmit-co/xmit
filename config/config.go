package config

type Redirect struct {
	From      string `toml:"from"`
	To        string `toml:"to"`
	Permanent bool   `toml:"permanent"`
}

type Header struct {
	Name  string  `toml:"name"`
	Value *string `toml:"value"`
	On    *string `toml:"on"`
}

type Form struct {
	From string `toml:"from"`
	To   string `toml:"to"`
	Then string `toml:"then"`
}

type XmitTOML struct {
	Fallback  string     `toml:"fallback"`
	FourOFour string     `toml:"404"`
	Headers   []Header   `toml:"headers"`
	Redirects []Redirect `toml:"redirects"`
	Forms     []Form     `toml:"forms"`
}
