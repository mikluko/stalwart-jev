package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfig(t *testing.T) {
	_, err := loadConfig(env(nil))
	require.ErrorContains(t, err, "TYPESAFE_API_KEY")

	c, err := loadConfig(env(map[string]string{"TYPESAFE_API_KEY": "k"}))
	require.NoError(t, err)
	require.Equal(t, config{listen: ":8080", url: "https://api.typesafe.ai/v1/systemone", model: "jev-latest", key: "k",
		maxBody: 32 << 10, high: 0.8, medium: 0.5, timeout: 20 * time.Second}, c)

	_, err = loadConfig(env(map[string]string{"TYPESAFE_API_KEY": "k", "CONFIDENCE_MEDIUM": "0.9"}))
	require.ErrorContains(t, err, "above CONFIDENCE_HIGH")
}
