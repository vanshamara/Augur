package config

import "testing"

// FuzzParse checks that the JSON config parser never panics on arbitrary input.
// It must always return either a valid App or an error.
func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"backends":[{"id":"a","model":"model-a"}]}`))
	f.Add([]byte(`{"backends":[{"model":"m"}],"router":{"type":"bandit"}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"backends":[]}`))
	f.Add([]byte(`not json`))
	f.Add([]byte(``))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
