package sqlite

import (
	"encoding/json"

	"github.com/ArionMiles/alfred"
	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// invocationInputSchemaVersion identifies the durable invocation-input format.
// Reading an earlier version after changing it requires an explicit decoder.
const invocationInputSchemaVersion = 1

type invocationInputRecord struct {
	Version uint32                    `json:"version"`
	Parts   []invocationContentRecord `json:"parts"`
}

type invocationContentRecord struct {
	Kind     alfred.ContentKind        `json:"kind"`
	Text     string                    `json:"text,omitempty"`
	Resource *invocationResourceRecord `json:"resource,omitempty"`
}

type invocationResourceRecord struct {
	URI       string `json:"uri"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
}

func marshalInvocationInput(parts []alfred.ContentPart) ([]byte, error) {
	const op = "marshalInvocationInput"

	records := make([]invocationContentRecord, 0, len(parts))
	if parts == nil {
		records = nil
	}
	for _, part := range parts {
		switch part := part.(type) {
		case alfred.TextContent:
			records = append(records, invocationContentRecord{
				Kind: alfred.ContentKindText,
				Text: part.Text,
			})
		case alfred.ResourceContent:
			switch part.Type {
			case alfred.ContentKindImage,
				alfred.ContentKindAudio,
				alfred.ContentKindVideo,
				alfred.ContentKindFile:
				// Supported resource kinds share the record mapping below.
			default:
				return nil, errors.B.
					Op(op).
					KindInvalidInput().
					Textf("unsupported resource content kind %q", part.Type).
					Build()
			}
			records = append(records, invocationContentRecord{
				Kind: part.Type,
				Resource: &invocationResourceRecord{
					URI:       part.Resource.URI,
					MediaType: part.Resource.MediaType,
					Name:      part.Resource.Name,
				},
			})
		default:
			return nil, errors.B.
				Op(op).
				KindInvalidInput().
				Textf("unsupported invocation content %T", part).
				Build()
		}
	}

	data, err := json.Marshal(invocationInputRecord{
		Version: invocationInputSchemaVersion,
		Parts:   records,
	})
	if err != nil {
		return nil, errors.B.Op(op).Text("encode invocation input").Err(err).Build()
	}
	return data, nil
}

func unmarshalInvocationInput(data []byte) ([]alfred.ContentPart, error) {
	const op = "unmarshalInvocationInput"

	var record invocationInputRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, errors.B.
			Op(op).
			KindInvalidInput().
			Text("decode invocation input").
			Err(err).
			Build()
	}
	if record.Version != invocationInputSchemaVersion {
		return nil, errors.B.
			Op(op).
			KindInvalidInput().
			Textf("invocation input has schema version %d", record.Version).
			Build()
	}

	parts := make([]alfred.ContentPart, 0, len(record.Parts))
	if record.Parts == nil {
		parts = nil
	}
	for _, part := range record.Parts {
		switch part.Kind {
		case alfred.ContentKindText:
			parts = append(parts, alfred.TextContent{Text: part.Text})
		case alfred.ContentKindImage,
			alfred.ContentKindAudio,
			alfred.ContentKindVideo,
			alfred.ContentKindFile:
			if part.Resource == nil {
				return nil, errors.B.
					Op(op).
					KindInvalidInput().
					Textf("%q invocation content has no resource", part.Kind).
					Build()
			}
			parts = append(parts, alfred.ResourceContent{
				Type: part.Kind,
				Resource: alfred.ResourceRef{
					URI:       part.Resource.URI,
					MediaType: part.Resource.MediaType,
					Name:      part.Resource.Name,
				},
			})
		default:
			return nil, errors.B.
				Op(op).
				KindInvalidInput().
				Textf("unsupported invocation content kind %q", part.Kind).
				Build()
		}
	}
	return parts, nil
}
