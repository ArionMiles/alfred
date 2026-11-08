package openai

import (
	"context"
	"encoding/json"
	"net/http"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

func requestError(ctx context.Context, err error) error {
	const op = "requestError"

	if ctx.Err() != nil {
		return errors.B.Op(op).Err(ctx.Err()).Build()
	}
	// SDK errors may contain response bodies, request URLs, or provider-echoed
	// secrets. Retain only classifications we control, not the raw error chain.
	var apiError *sdk.Error
	if errors.As(err, &apiError) {
		return statusError(apiError.StatusCode)
	}
	var streamError *ssestream.StreamError
	if errors.As(err, &streamError) {
		var body struct {
			Code  string `json:"code"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(streamError.Event.Data, &body) == nil {
			if body.Code == "" {
				body.Code = body.Error.Code
			}
			return responseError(body.Code)
		}
	}
	return errors.B.Op(op).KindUnavailable().Retryable(true).UserMsg("The connection to OpenAI failed.").Build()
}

func statusError(status int) error {
	const op = "statusError"

	builder := errors.B.Op(op).Textf("OpenAI HTTP status %d", status)
	switch status {
	case http.StatusUnauthorized:
		return builder.KindUnauthenticated().Retryable(true).UserMsg("OpenAI rejected the credential. Update it before resuming.").Build()
	case http.StatusForbidden:
		return builder.KindPermissionDenied().UserMsg("OpenAI denied access to this request.").Build()
	case http.StatusTooManyRequests:
		return builder.KindResourceExhausted().Retryable(true).UserMsg("OpenAI's usage or rate limit was reached. Check your account before resuming.").Build()
	case http.StatusRequestTimeout, http.StatusConflict:
		return builder.KindUnavailable().Retryable(true).UserMsg("OpenAI could not process this request. Try again.").Build()
	default:
		if status >= http.StatusInternalServerError {
			return builder.KindUnavailable().Retryable(true).UserMsg("OpenAI is temporarily unavailable.").Build()
		}
		return builder.Kind(errors.KindFromStatus(status)).UserMsg("OpenAI rejected this request.").Build()
	}
}

func responseError(code string) error {
	const op = "responseError"

	switch code {
	case "rate_limit_exceeded", "insufficient_quota":
		return statusError(http.StatusTooManyRequests)
	case "invalid_api_key", "invalid_authentication":
		return statusError(http.StatusUnauthorized)
	case "server_error", "vector_store_timeout":
		return statusError(http.StatusServiceUnavailable)
	case "invalid_prompt", "invalid_request_error":
		return statusError(http.StatusBadRequest)
	default:
		return errors.B.Op(op).KindBadGateway().UserMsg("OpenAI could not complete this response.").Build()
	}
}
