package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Spec is the subset of PortOne's OpenAPI document the generator reads.
type Spec struct {
	Info struct {
		Version string `json:"version"`
	} `json:"info"`
	Paths      ordered[ordered[*Operation]] `json:"paths"`
	Components struct {
		Schemas ordered[*Schema] `json:"schemas"`
	} `json:"components"`
}

type Schema struct {
	Ref                  string              `json:"$ref"`
	Type                 string              `json:"type"`
	Format               string              `json:"format"`
	Title                string              `json:"title"`
	Description          string              `json:"description"`
	Enum                 []string            `json:"enum"`
	EnumMeta             map[string]enumMeta `json:"x-portone-enum"`
	OneOf                []*Schema           `json:"oneOf"`
	Discriminator        *discriminator      `json:"discriminator"`
	Properties           ordered[*Schema]    `json:"properties"`
	Required             []string            `json:"required"`
	Items                *Schema             `json:"items"`
	AdditionalProperties *Schema             `json:"additionalProperties"`
}

type enumMeta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type discriminator struct {
	PropertyName string          `json:"propertyName"`
	Mapping      ordered[string] `json:"mapping"`
}

type Operation struct {
	OperationID string       `json:"operationId"`
	Summary     string       `json:"summary"`
	Description string       `json:"description"`
	Parameters  []*Parameter `json:"parameters"`
	RequestBody *Body        `json:"requestBody"`
	Responses   map[string]*Body
	Category    string `json:"x-portone-category"`
	Error       struct {
		Ref string `json:"$ref"`
	} `json:"x-portone-error"`
	Unstable bool `json:"x-portone-unstable"`
}

type Parameter struct {
	Name        string                `json:"name"`
	In          string                `json:"in"`
	Description string                `json:"description"`
	Title       string                `json:"x-portone-title"`
	Required    bool                  `json:"required"`
	Schema      *Schema               `json:"schema"`
	Content     map[string]*mediaType `json:"content"`
}

type Body struct {
	Required bool                  `json:"required"`
	Content  map[string]*mediaType `json:"content"`
}

type mediaType struct {
	Schema *Schema `json:"schema"`
}

// refName strips "#/components/schemas/" from a $ref.
func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// ordered is a JSON object decoded with its key order kept, so generated code follows the spec's order.
type ordered[T any] []entry[T]

type entry[T any] struct {
	Key   string
	Value T
}

func (o *ordered[T]) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("expected object, got %v (%v)", t, err)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		var v T
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("%v: %w", t, err)
		}
		*o = append(*o, entry[T]{t.(string), v})
	}
	return nil
}
