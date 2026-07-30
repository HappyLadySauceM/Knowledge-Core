package client

import (
	"errors"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/discovery"
)

const KnowledgeServiceName = "knowledge-core.knowledge"

func NewKnowledge(resolver discovery.Resolver, runtime *observability.Runtime) (knowledgeservice.Client, error) {
	if resolver == nil {
		return nil, errors.New("create Knowledge client: resolver is required")
	}
	if runtime == nil {
		return nil, errors.New("create Knowledge client: observability runtime is required")
	}
	options := []kitexclient.Option{
		kitexclient.WithResolver(resolver),
		kitexclient.WithConnectTimeout(500 * time.Millisecond),
		kitexclient.WithRPCTimeout(3 * time.Second),
	}
	options = append(options, observability.KitexClientOptions(runtime)...)
	client, err := knowledgeservice.NewClient(KnowledgeServiceName, options...)
	if err != nil {
		return nil, err
	}
	return client, nil
}
