package appconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestAppConfigValidate_invalid(t *testing.T) {
	tt := map[string]*AppConfig{
		"empty": {},
		"token and app config": {
			AppID:             "1",
			AppInstallationID: 2,
			AppPrivateKey:     "private key",
			Token:             "token",
		},
		"app id not set": {
			AppInstallationID: 2,
			AppPrivateKey:     "private key",
		},
		"app installation id not set": {
			AppID:         "2",
			AppPrivateKey: "private key",
		},
		"private key empty": {
			AppID:             "2",
			AppInstallationID: 1,
			AppPrivateKey:     "",
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			require.Error(t, err)
		})
	}
}

func TestAppConfigValidate_valid(t *testing.T) {
	tt := map[string]*AppConfig{
		"token": {
			Token: "token",
		},
		"app ID": {
			AppID:             "1",
			AppInstallationID: 2,
			AppPrivateKey:     "private key",
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			err := cfg.Validate()
			require.NoError(t, err)
		})
	}
}

func TestAppConfigFromSecret_invalid(t *testing.T) {
	tt := map[string]map[string]string{
		"empty": {},
		"token and app provided": {
			"github_token":              "token",
			"github_app_id":             "2",
			"githu_app_installation_id": "3",
			"github_app_private_key":    "private key",
		},
		"invalid app id": {
			"github_app_id":             "abc",
			"githu_app_installation_id": "3",
			"github_app_private_key":    "private key",
		},
		"invalid app installation_id": {
			"github_app_id":             "1",
			"githu_app_installation_id": "abc",
			"github_app_private_key":    "private key",
		},
		"empty private key": {
			"github_app_id":             "1",
			"githu_app_installation_id": "2",
			"github_app_private_key":    "",
		},
	}

	for name, data := range tt {
		t.Run(name, func(t *testing.T) {
			secret := &corev1.Secret{
				StringData: data,
			}

			appConfig, err := FromSecret(secret)
			assert.Error(t, err)
			assert.Nil(t, appConfig)
		})
	}
}

func TestAppConfigFromSecret_valid(t *testing.T) {
	tt := map[string]map[string]string{
		"with token": {
			"github_token": "token",
		},
		"app config": {
			"github_app_id":             "2",
			"githu_app_installation_id": "3",
			"github_app_private_key":    "private key",
		},
	}

	for name, data := range tt {
		t.Run(name, func(t *testing.T) {
			secret := &corev1.Secret{
				StringData: data,
			}

			appConfig, err := FromSecret(secret)
			assert.Error(t, err)
			assert.Nil(t, appConfig)
		})
	}
}

func TestAppConfigFromString_valid(t *testing.T) {
	tt := map[string]*AppConfig{
		"token": {
			Token: "token",
		},
		"app ID": {
			AppID:             "1",
			AppInstallationID: 2,
			AppPrivateKey:     "private key",
		},
	}

	for name, cfg := range tt {
		t.Run(name, func(t *testing.T) {
			bytes, err := json.Marshal(cfg)
			require.NoError(t, err)

			got, err := FromJSONString(string(bytes))
			require.NoError(t, err)

			want := cfg.tidy()
			assert.Equal(t, want, got)
		})
	}
}
