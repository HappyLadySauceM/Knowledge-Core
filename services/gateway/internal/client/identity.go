package client

import (
	"errors"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/discovery"
)

const IdentityServiceName = "knowledge-core.identity"

func NewIdentity(resolver discovery.Resolver) (identityservice.Client, error) {
	if resolver == nil {
		return nil, errors.New("create Identity client: resolver is required")
	}
	client, err := identityservice.NewClient(
		IdentityServiceName,
		kitexclient.WithResolver(resolver),
		kitexclient.WithConnectTimeout(500*time.Millisecond),
		kitexclient.WithRPCTimeout(3*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return client, nil
}
