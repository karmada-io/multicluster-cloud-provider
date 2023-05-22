package app

import (
	"fmt"

	controllerscontext "github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/context"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/crdsynchronizer"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/multiclusteringress"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/serviceexport"
)

func startMCIController(ctx controllerscontext.Context) (enabled bool, err error) {
	loadBalancer, support := ctx.CloudProvider.LoadBalancer()
	if !support {
		return false, fmt.Errorf("the multicluster controller manager does not support external loadBalancer")
	}
	if loadBalancer == nil {
		return false, fmt.Errorf("clould get the target external loadBalancer provider")
	}

	mciController := &multiclusteringress.MCIController{
		Client:             ctx.Mgr.GetClient(),
		LoadBalancer:       loadBalancer,
		InformerManager:    ctx.InformerManager,
		EventRecorder:      ctx.Mgr.GetEventRecorderFor(multiclusteringress.ControllerName),
		RateLimiterOptions: ctx.Opts.RateLimiterOptions,
		ProviderClassName:  ctx.ProviderClassName,
	}
	if err = mciController.SetupWithManager(ctx.Context, ctx.Mgr); err != nil {
		return false, err
	}
	return true, nil
}

func startCRDSynchronizer(ctx controllerscontext.Context) (enabled bool, err error) {
	crdSynchronizer := &crdsynchronizer.CRDSynchronizer{
		Client:             ctx.Mgr.GetClient(),
		EventRecorder:      ctx.Mgr.GetEventRecorderFor(crdsynchronizer.ControllerName),
		RateLimiterOptions: ctx.Opts.RateLimiterOptions,
	}
	if err = crdSynchronizer.SetupWithManager(ctx.Context, ctx.Mgr); err != nil {
		return false, err
	}
	return true, nil
}

func startServiceExportController(ctx controllerscontext.Context) (enabled bool, err error) {
	c := &serviceexport.Controller{
		Client:             ctx.Mgr.GetClient(),
		EventRecorder:      ctx.Mgr.GetEventRecorderFor(serviceexport.ControllerName),
		RateLimiterOptions: ctx.Opts.RateLimiterOptions,
		ProviderClassName:  ctx.ProviderClassName,
	}
	if err = c.SetupWithManager(ctx.Context, ctx.Mgr); err != nil {
		return false, err
	}
	return true, nil
}
