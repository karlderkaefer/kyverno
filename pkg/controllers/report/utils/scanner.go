package utils

import (
	"context"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	"github.com/kyverno/kyverno/pkg/clients/dclient"
	"github.com/kyverno/kyverno/pkg/config"
	"github.com/kyverno/kyverno/pkg/engine"
	engineapi "github.com/kyverno/kyverno/pkg/engine/api"
	enginecontext "github.com/kyverno/kyverno/pkg/engine/context"
	"github.com/kyverno/kyverno/pkg/registryclient"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type scanner struct {
	logger  logr.Logger
	engine  engineapi.Engine
	client  dclient.Interface
	rclient registryclient.Client
	config  config.Configuration
}

type ScanResult struct {
	EngineResponse *engineapi.EngineResponse
	Error          error
}

type Scanner interface {
	ScanResource(context.Context, unstructured.Unstructured, map[string]string, ...kyvernov1.PolicyInterface) map[kyvernov1.PolicyInterface]ScanResult
}

func NewScanner(
	logger logr.Logger,
	engine engineapi.Engine,
	client dclient.Interface,
	rclient registryclient.Client,
	config config.Configuration,
) Scanner {
	return &scanner{
		logger:  logger,
		engine:  engine,
		client:  client,
		rclient: rclient,
		config:  config,
	}
}

func (s *scanner) ScanResource(ctx context.Context, resource unstructured.Unstructured, nsLabels map[string]string, policies ...kyvernov1.PolicyInterface) map[kyvernov1.PolicyInterface]ScanResult {
	results := map[kyvernov1.PolicyInterface]ScanResult{}
	for _, policy := range policies {
		var errors []error
		response, err := s.validateResource(ctx, resource, nsLabels, policy)
		if err != nil {
			s.logger.Error(err, "failed to scan resource")
			errors = append(errors, err)
		}
		spec := policy.GetSpec()
		if spec.HasVerifyImages() {
			ivResponse, err := s.validateImages(ctx, resource, nsLabels, policy)
			if err != nil {
				s.logger.Error(err, "failed to scan images")
				errors = append(errors, err)
			}
			if response == nil {
				response = ivResponse
			} else {
				response.PolicyResponse.Rules = append(response.PolicyResponse.Rules, ivResponse.PolicyResponse.Rules...)
			}
		}
		results[policy] = ScanResult{response, multierr.Combine(errors...)}
	}
	return results
}

func (s *scanner) validateResource(ctx context.Context, resource unstructured.Unstructured, nsLabels map[string]string, policy kyvernov1.PolicyInterface) (*engineapi.EngineResponse, error) {
	enginectx := enginecontext.NewContext()
	if err := enginectx.AddResource(resource.Object); err != nil {
		return nil, err
	}
	if err := enginectx.AddNamespace(resource.GetNamespace()); err != nil {
		return nil, err
	}
	if err := enginectx.AddImageInfos(&resource, s.config); err != nil {
		return nil, err
	}
	if err := enginectx.AddOperation("CREATE"); err != nil {
		return nil, err
	}
	policyCtx := engine.NewPolicyContextWithJsonContext(enginectx).
		WithNewResource(resource).
		WithPolicy(policy).
		WithClient(s.client).
		WithNamespaceLabels(nsLabels)
	return s.engine.Validate(ctx, policyCtx), nil
}

func (s *scanner) validateImages(ctx context.Context, resource unstructured.Unstructured, nsLabels map[string]string, policy kyvernov1.PolicyInterface) (*engineapi.EngineResponse, error) {
	enginectx := enginecontext.NewContext()
	if err := enginectx.AddResource(resource.Object); err != nil {
		return nil, err
	}
	if err := enginectx.AddNamespace(resource.GetNamespace()); err != nil {
		return nil, err
	}
	if err := enginectx.AddImageInfos(&resource, s.config); err != nil {
		return nil, err
	}
	if err := enginectx.AddOperation("CREATE"); err != nil {
		return nil, err
	}
	policyCtx := engine.NewPolicyContextWithJsonContext(enginectx).
		WithNewResource(resource).
		WithPolicy(policy).
		WithClient(s.client).
		WithNamespaceLabels(nsLabels)
	response, _ := s.engine.VerifyAndPatchImages(ctx, s.rclient, policyCtx)
	if len(response.PolicyResponse.Rules) > 0 {
		s.logger.Info("validateImages", "policy", policy, "response", response)
	}
	return response, nil
}
