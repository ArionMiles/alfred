package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
		if strings.HasPrefix(apiError.Code, "subscription_sharing_") || strings.HasPrefix(apiError.Code, "chatpass_v2_") {
			return responseError(apiError.Code)
		}
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
	case "subscription_sharing_usage_limit_exceeded":
		return errors.B.Op(op).KindResourceExhausted().Retryable(true).
			UserMsg("ChatGPT plan usage is limited. Check the app and plan limits at https://chatgpt.com/settings/usage before resuming.").Build()
	case "invalid_api_key", "invalid_authentication":
		return statusError(http.StatusUnauthorized)
	case "subscription_sharing_invalid_user":
		return errors.B.Op(op).KindUnauthenticated().Retryable(true).
			UserMsg("OpenAI could not validate the ChatGPT account. Check the connection in Providers; reconnect if access was revoked.").Build()
	case "server_error", "vector_store_timeout":
		return statusError(http.StatusServiceUnavailable)
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return errors.B.Op(op).KindUnavailable().Retryable(true).
			UserMsg("OpenAI could not check ChatGPT account availability. Wait and try again; your connection has been kept.").Build()
	case "invalid_prompt", "invalid_request_error":
		return statusError(http.StatusBadRequest)
	case "subscription_sharing_unsupported_capability":
		return errors.B.Op(op).KindInvalidArgument().
			UserMsg("This request uses a capability unavailable through ChatGPT plan access. Change the request or model before trying again.").Build()
	case "subscription_sharing_user_not_eligible":
		return errors.B.Op(op).KindPermissionDenied().
			UserMsg("ChatGPT plan usage is unavailable for this account or workspace policy. Check eligibility or configure an API key in Providers.").Build()
	case "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return errors.B.Op(op).KindPermissionDenied().
			UserMsg("The OpenAI grant does not authorize this operation. Check the selected account and ChatGPT plan permission in Providers.").Build()
	case "subscription_sharing_route_not_supported":
		return errors.B.Op(op).KindFailedPrecondition().
			UserMsg("This OpenAI operation is not available through ChatGPT plan access.").Build()
	default:
		return errors.B.Op(op).KindBadGateway().UserMsg("OpenAI could not complete this response.").Build()
	}
}

func subscriptionHTTPError(response *http.Response) error {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&body)
	if strings.HasPrefix(body.Error.Code, "subscription_sharing_") || strings.HasPrefix(body.Error.Code, "chatpass_v2_") {
		return responseError(body.Error.Code)
	}
	return statusError(response.StatusCode)
}
