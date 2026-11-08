package alfred

// ContentKind identifies the modality of a content part.
type ContentKind string

const (
	// ContentKindText identifies text content.
	ContentKindText ContentKind = "text"
	// ContentKindImage identifies image content.
	ContentKindImage ContentKind = "image"
	// ContentKindAudio identifies audio content.
	ContentKindAudio ContentKind = "audio"
	// ContentKindVideo identifies video content.
	ContentKindVideo ContentKind = "video"
	// ContentKindFile identifies other file content.
	ContentKindFile ContentKind = "file"
)

// ContentPart is one part of multimodal content.
type ContentPart interface {
	Kind() ContentKind
}

// TextContent contains text.
type TextContent struct {
	Text string
}

// Kind returns ContentKindText.
func (TextContent) Kind() ContentKind {
	return ContentKindText
}

// ResourceRef identifies content that can be resolved by Alfred.
type ResourceRef struct {
	URI       string
	MediaType string
	Name      string
}

// ResourceContent contains non-text content referenced by URI.
type ResourceContent struct {
	Type     ContentKind
	Resource ResourceRef
}

// Kind returns the resource's modality.
func (content ResourceContent) Kind() ContentKind {
	return content.Type
}
