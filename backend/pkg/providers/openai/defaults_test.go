package openai

import "github.com/creasty/defaults"

func withDefaults[T any]() T {
	var value T
	defaults.MustSet(&value)
	return value
}
