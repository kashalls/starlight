// Package config loads starlight's runtime configuration from environment
// variables.
package config

import "github.com/caarlos0/env/v11"

type Config struct {
	Port          string `env:"PORT" envDefault:"8080"`
	DataDir       string `env:"DATA_DIR" envDefault:"/data"`
	PublicBaseURL string `env:"PUBLIC_BASE_URL,required,notEmpty"`
	UploadToken   string `env:"UPLOAD_TOKEN,required,notEmpty"`
	MaxUploadSize int64  `env:"MAX_UPLOAD_SIZE" envDefault:"104857600"`
}

func Load() (*Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
