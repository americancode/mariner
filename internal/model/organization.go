package model

type Organization struct {
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	Groups      []string                          `json:"groups"`
	Provisioned bool                              `json:"provisioned"`
	Connections map[string]OrganizationConnection `json:"connections"`
}

type OrganizationConnection struct {
	Connection
	AccessKeyEnv string `json:"accessKeyEnv,omitempty"`
	SecretKeyEnv string `json:"secretKeyEnv,omitempty"`
}
