package http

import (
	"embed"
	"encoding/json"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openAPIYAML embed.FS

var (
	openAPIJSON     []byte
	openAPIJSONOnce sync.Once
	openAPIJSONErr  error
)

// getOpenAPIJSON returns the OpenAPI specification as JSON.
// The YAML is converted to JSON on first access and cached.
func getOpenAPIJSON() ([]byte, error) {
	openAPIJSONOnce.Do(func() {
		openAPIJSON, openAPIJSONErr = convertOpenAPIToJSON()
	})
	return openAPIJSON, openAPIJSONErr
}

// convertOpenAPIToJSON reads the embedded YAML and converts it to JSON.
func convertOpenAPIToJSON() ([]byte, error) {
	yamlData, err := openAPIYAML.ReadFile("openapi.yaml")
	if err != nil {
		return nil, err
	}

	var spec interface{}
	if err := yaml.Unmarshal(yamlData, &spec); err != nil {
		return nil, err
	}

	// Convert YAML structure to JSON-compatible structure
	spec = convertYAMLToJSON(spec)

	return json.MarshalIndent(spec, "", "  ")
}

// convertYAMLToJSON recursively converts YAML map keys to strings
// (YAML uses interface{} keys, JSON requires string keys).
func convertYAMLToJSON(v interface{}) interface{} {
	switch v := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, value := range v {
			result[key] = convertYAMLToJSON(value)
		}
		return result
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(v))
		for key, value := range v {
			strKey, ok := key.(string)
			if !ok {
				continue
			}
			result[strKey] = convertYAMLToJSON(value)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(v))
		for i, value := range v {
			result[i] = convertYAMLToJSON(value)
		}
		return result
	default:
		return v
	}
}
