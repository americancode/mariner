package model

// Data is the decrypted per-user vault document.
type Data struct {
	Connections []Connection `json:"connections"`
	Settings    Settings     `json:"settings"`
}

type Settings struct {
	Theme string `json:"theme,omitempty"`
}
