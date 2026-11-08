package rpc

import (
	"connectrpc.com/connect/v2"

	"github.com/ArionMiles/alfred/pkg/x/errors"
)

// maxPageSize is part of the RPC contract documented in the Protobuf schema.
const maxPageSize uint32 = 100

func connectError(err error) error {
	code := connect.CodeInternal
	switch errors.WhatKind(err) {
	case errors.Canceled:
		code = connect.CodeCanceled
	case errors.InvalidArgument, errors.InvalidInput:
		code = connect.CodeInvalidArgument
	case errors.DeadlineExceeded:
		code = connect.CodeDeadlineExceeded
	case errors.NotFound:
		code = connect.CodeNotFound
	case errors.Conflict, errors.FailedPrecondition:
		code = connect.CodeFailedPrecondition
	case errors.PermissionDenied:
		code = connect.CodePermissionDenied
	case errors.Unauthenticated:
		code = connect.CodeUnauthenticated
	case errors.ResourceExhausted, errors.PayloadTooLarge:
		code = connect.CodeResourceExhausted
	case errors.Unimplemented, errors.MethodNotAllowed:
		code = connect.CodeUnimplemented
	case errors.Unavailable, errors.BadGateway:
		code = connect.CodeUnavailable
	}

	message := errors.UserMsg(err)
	if message == "" {
		message = defaultErrorMessage(code)
	}
	return connect.NewError(code, message).WithCause(err)
}

func defaultErrorMessage(code connect.Code) string {
	switch code {
	case connect.CodeCanceled:
		return "the request was cancelled"
	case connect.CodeInvalidArgument:
		return "the request is invalid"
	case connect.CodeDeadlineExceeded:
		return "the request timed out"
	case connect.CodeNotFound:
		return "the requested resource was not found"
	case connect.CodeFailedPrecondition:
		return "the request conflicts with the current state"
	case connect.CodePermissionDenied:
		return "permission was denied"
	case connect.CodeUnauthenticated:
		return "authentication is required"
	case connect.CodeResourceExhausted:
		return "a resource limit was reached"
	case connect.CodeUnimplemented:
		return "the operation is not supported"
	case connect.CodeUnavailable:
		return "the service is unavailable"
	default:
		return "the request failed"
	}
}
