package app

import (
	"fmt"

	controllerscontext "github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/context"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/crdinstallation"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/multiclusteringress"
	"github.com/karmada-io/multicluster-cloud-provider/pkg/controllers/serviceexportpropagation"
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

func startCRDInstallationController(ctx controllerscontext.Context) (enabled bool, err error) {
	c := &crdinstallation.Controller{
		Client:             ctx.Mgr.GetClient(),
		EventRecorder:      ctx.Mgr.GetEventRecorderFor(crdinstallation.ControllerName),
		RateLimiterOptions: ctx.Opts.RateLimiterOptions,
	}
	if err = c.SetupWithManager(ctx.Context, ctx.Mgr); err != nil {
		return false, err
	}
	return true, nil
}

func startServiceExportPropagationController(ctx controllerscontext.Context) (enabled bool, err error) {
	c := &serviceexportpropagation.Controller{
		Client:             ctx.Mgr.GetClient(),
		EventRecorder:      ctx.Mgr.GetEventRecorderFor(serviceexportpropagation.ControllerName),
		RateLimiterOptions: ctx.Opts.RateLimiterOptions,
		ProviderClassName:  ctx.ProviderClassName,
	}
	if err = c.SetupWithManager(ctx.Context, ctx.Mgr); err != nil {
		return false, err
	}
	return true, nil
}
