// Package raster implements the output.SpatialSource port for raster bundles:
// a ZIP containing a manifest (ortus-raster.yaml), one or more Cloud Optimized
// GeoTIFFs, and an integer-value -> attribute mapping. See doc/raster-bundle/.
package raster

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	yaml "gopkg.in/yaml.v3"
)

// manifestName is the fixed manifest filename at the bundle (ZIP) root.
const manifestName = "ortus-raster.yaml"

//go:embed ortus-raster.schema.json
var schemaJSON []byte

// resolvedSchema is the compiled bundle manifest schema, shared across Opens.
var resolvedSchema *jsonschema.Resolved

func init() {
	var s jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &s); err != nil {
		panic("raster: invalid embedded schema: " + err.Error())
	}
	r, err := s.Resolve(nil)
	if err != nil {
		panic("raster: cannot resolve embedded schema: " + err.Error())
	}
	resolvedSchema = r
}

// manifest is the typed view of ortus-raster.yaml.
type manifest struct {
	SchemaVersion int         `yaml:"schema_version"`
	ID            string      `yaml:"id"`
	Name          string      `yaml:"name"`
	Description   string      `yaml:"description"`
	License       licenseSpec `yaml:"license"`
	CRS           string      `yaml:"crs"`
	Layers        []layerSpec `yaml:"layers"`
}

type licenseSpec struct {
	Name        string `yaml:"name"`
	Attribution string `yaml:"attribution"`
	URL         string `yaml:"url"`
}

type layerSpec struct {
	ID           string                         `yaml:"id"`
	Description  string                         `yaml:"description"`
	File         string                         `yaml:"file"`
	Band         int                            `yaml:"band"`
	Nodata       *float64                       `yaml:"nodata"`
	Sampling     string                         `yaml:"sampling"`
	Mapping      map[int]map[string]interface{} `yaml:"mapping"`
	ValueMapping string                         `yaml:"value_mapping"`
}

// parseAndValidateManifest schema-validates the raw manifest (against the
// embedded JSON Schema — the same one the pipeline validates against) and then
// decodes it into the typed manifest with unknown-field rejection.
func parseAndValidateManifest(raw []byte) (*manifest, error) {
	if err := validateAgainstSchema(raw); err != nil {
		return nil, fmt.Errorf("schema validation: %w", err)
	}

	var m manifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &m, nil
}

// validateAgainstSchema validates the raw YAML against the embedded schema by
// normalizing it to a JSON value (string keys, JSON number types) first.
func validateAgainstSchema(raw []byte) error {
	var y interface{}
	if err := yaml.Unmarshal(raw, &y); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}
	jsonBytes, err := json.Marshal(stringifyKeys(y))
	if err != nil {
		return err
	}
	var instance interface{}
	if err := json.Unmarshal(jsonBytes, &instance); err != nil {
		return err
	}
	return resolvedSchema.Validate(instance)
}

// stringifyKeys recursively converts map keys to strings so a YAML document
// (which permits non-string keys, e.g. integer mapping keys) becomes a valid
// JSON value the schema validator can consume.
func stringifyKeys(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = stringifyKeys(val)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprintf("%v", k)] = stringifyKeys(val)
		}
		return out
	case []interface{}:
		for i := range t {
			t[i] = stringifyKeys(t[i])
		}
		return t
	default:
		return v
	}
}

// resolveMapping builds the pixel-value -> properties table for a layer from
// either the inline mapping or the sidecar file referenced by value_mapping.
func resolveMapping(spec layerSpec, readSidecar func(name string) ([]byte, error)) (map[int64]map[string]interface{}, error) {
	out := make(map[int64]map[string]interface{})

	if spec.Mapping != nil {
		for k, v := range spec.Mapping {
			out[int64(k)] = v
		}
		return out, nil
	}

	if spec.ValueMapping == "" {
		return nil, fmt.Errorf("layer %q has neither mapping nor value_mapping", spec.ID)
	}

	data, err := readSidecar(spec.ValueMapping)
	if err != nil {
		return nil, fmt.Errorf("reading value_mapping %q: %w", spec.ValueMapping, err)
	}
	// JSON is a subset of YAML, so yaml.v3 parses both .json and .yaml sidecars.
	// Keys arrive as strings (JSON) or ints (YAML); normalize via string.
	var raw map[string]map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing value_mapping %q: %w", spec.ValueMapping, err)
	}
	for ks, v := range raw {
		k, err := strconv.ParseInt(ks, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("value_mapping %q: non-integer key %q", spec.ValueMapping, ks)
		}
		out[k] = v
	}
	return out, nil
}
