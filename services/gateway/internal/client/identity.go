package client

import (
	"errors"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/discovery"
)

const IdentityServiceName = "knowledge-core.identity"

func NewIdentity(resolver discovery.Resolver, runtime *observability.Runtime) (identityservice.Client, error) {
	if resolver == nil {
		return nil, errors.New("create Identity client: resolver is required")
	}
	if runtime == nil {
		return nil, errors.New("create Identity client: observability runtime is required")
	}
	client, err := identityservice.NewClient(
		IdentityServiceName,
		kitexclient.WithResolver(resolver),
		kitexclient.WithConnectTimeout(500*time.Millisecond),
		kitexclient.WithRPCTimeout(3*time.Second),
		kitexclient.WithMiddleware(observability.KitexClientMiddleware(runtime)),
	)
	if err != nil {
		return nil, err
	}
	return client, nil
}
