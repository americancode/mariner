package dto

// ConnectionRequest is the HTTP input shape for creating or updating an S3
// connection. Secrets are accepted only on write requests.
type ConnectionRequest struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Prefix    string `json:"prefix"`
}

// ConnectionResponse is the safe UI/API representation. It deliberately does
// not contain access or secret keys.
type ConnectionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Bucket   string `json:"bucket"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
	Prefix   string `json:"prefix"`
}

type ConnectionIDResponse struct {
	ID string `json:"id"`
}
