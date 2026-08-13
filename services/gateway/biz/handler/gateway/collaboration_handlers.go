package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"time"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const (
	collaborationSubprotocol = "knowledge-core-yjs-v1"
	collaborationFragment    = "default"
)

func handleCreateCollaborationSession(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	if pathErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	principal, principalOK := gatewaymiddleware.Principal(request)
	_, tokenOK := gatewaymiddleware.AccessToken(request)
	if !ok || !principalOK || !tokenOK {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	session, err := dependencies.Collaboration.CreateSession(
		upstreamContext(ctx, request), &collaborationv1.CreateSessionRequest{DocumentId: documentID},
	)
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	now := time.Now().UTC()
	if dependencies.Now != nil {
		now = dependencies.Now().UTC()
	}
	data, err := toCollaborationSessionData(
		session, documentID, dependencies.EndpointOptions().CollaborationWebSocketBaseURL, principal.ExpiresAt, now,
	)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Cache-Control", "no-store")
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func toCollaborationSessionData(
	value *collaborationv1.CollaborationSession,
	documentID string,
	websocketBaseURL string,
	tokenExpiresAt time.Time,
	now time.Time,
) (*gatewaymodel.CollaborationSessionData, error) {
	if value == nil || value.Subprotocol != collaborationSubprotocol || value.Fragment != collaborationFragment ||
		(value.Access != "owner" && value.Access != "editor" && value.Access != "viewer") {
		return nil, errors.New("collaboration session is incomplete")
	}
	ticket, err := base64.RawURLEncoding.DecodeString(value.Ticket)
	if err != nil || len(ticket) != 32 || base64.RawURLEncoding.EncodeToString(ticket) != value.Ticket {
		return nil, errors.New("collaboration ticket is invalid")
	}
	ticketExpiry, ticketErr := time.Parse(time.RFC3339, value.TicketExpiresAt)
	sessionExpiry, sessionErr := time.Parse(time.RFC3339, value.SessionExpiresAt)
	if ticketErr != nil || sessionErr != nil || !ticketExpiry.After(now) || ticketExpiry.After(now.Add(time.Minute)) ||
		ticketExpiry.After(sessionExpiry) || !sessionExpiry.After(now) || tokenExpiresAt.IsZero() || sessionExpiry.After(tokenExpiresAt.UTC()) {
		return nil, errors.New("collaboration session expiry is invalid")
	}
	endpoint, err := url.Parse(websocketBaseURL)
	if err != nil || endpoint == nil {
		return nil, errors.New("collaboration WebSocket base URL is invalid")
	}
	path, err := collaborationWebSocketPath(value, documentID)
	if err != nil {
		return nil, err
	}
	endpoint.Path = path
	return &gatewaymodel.CollaborationSessionData{
		WebsocketURL: endpoint.String(), Ticket: value.Ticket, Subprotocol: value.Subprotocol,
		Fragment: value.Fragment, Access: value.Access, TicketExpiresAt: value.TicketExpiresAt,
		SessionExpiresAt: value.SessionExpiresAt,
	}, nil
}

// collaborationWebSocketPath builds the trusted instance WebSocket path from the RPC session.
// collaborationWebSocketPath 用 RPC 会话构造可信的实例 WebSocket 路径。
func collaborationWebSocketPath(value *collaborationv1.CollaborationSession, documentID string) (string, error) {
	if value == nil || !value.IsSetInstanceOrdinal() {
		return "", errors.New("collaboration instance ordinal is missing")
	}
	ordinal := value.GetInstanceOrdinal()
	if ordinal < 0 {
		return "", errors.New("collaboration instance ordinal is invalid")
	}
	return "/v1/instances/" + strconv.FormatInt(int64(ordinal), 10) + "/documents/" + url.PathEscape(documentID), nil
}
