package rpc

import (
	"github.com/ArionMiles/alfred"
	alfredv1 "github.com/ArionMiles/alfred/gen/alfred/v1"
)

func encodeSetupField(field alfred.SetupField) *alfredv1.SetupField {
	item := &alfredv1.SetupField{
		Id: field.ID, Label: field.Label, Description: field.Description,
		Kind: encodeSetupFieldKind(field.Kind), Required: field.Required,
	}
	for _, option := range field.Options {
		item.Options = append(item.Options, &alfredv1.SetupFieldOption{Value: option.Value, Label: option.Label})
	}
	return item
}

func encodeSetupFieldKind(kind alfred.SetupFieldKind) alfredv1.SetupFieldKind {
	switch kind {
	case alfred.SetupFieldText:
		return alfredv1.SetupFieldKind_SETUP_FIELD_KIND_TEXT
	case alfred.SetupFieldSecret:
		return alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SECRET
	case alfred.SetupFieldSelect:
		return alfredv1.SetupFieldKind_SETUP_FIELD_KIND_SELECT
	default:
		return alfredv1.SetupFieldKind_SETUP_FIELD_KIND_UNSPECIFIED
	}
}
