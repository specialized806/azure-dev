// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// ProvisioningProgressDisplay displays interactive progress for an ongoing Azure provisioning operation.
type ProvisioningProgressDisplay struct {
	// Whether the deployment has started
	deploymentStarted bool
	// Keeps track of created resources
	displayedResources map[string]bool
	resourceManager    ResourceManager
	console            input.Console
	deployment         Deployment
}

func NewProvisioningProgressDisplay(
	rm ResourceManager,
	console input.Console,
	deployment Deployment,
) *ProvisioningProgressDisplay {
	return &ProvisioningProgressDisplay{
		displayedResources: map[string]bool{},
		deployment:         deployment,
		resourceManager:    rm,
		console:            console,
	}
}

// ReportProgress reports the current deployment progress, setting the currently executing operation title and logging
// progress.
func (display *ProvisioningProgressDisplay) ReportProgress(
	ctx context.Context, queryStart *time.Time) error {
	if !display.deploymentStarted {
		_, err := display.deployment.Get(ctx)
		if err != nil {
			// Return default progress
			log.Printf("error while reporting progress: %v", err)
			return nil
		}

		display.deploymentStarted = true
		deploymentUrl, err := display.deployment.DeploymentUrl(ctx)
		if err != nil {
			return err
		}

		deploymentLink := fmt.Sprintf(output.WithLinkFormat("%s\n"), deploymentUrl)

		display.console.EnsureBlankLine(ctx)

		lines := []string{
			"You can view detailed progress in the Azure Portal:",
			deploymentLink,
		}

		if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
			lines = []string{
				"You can view detailed progress in the Azure Portal.",
				"\n",
			}
		}

		display.console.MessageUxItem(
			ctx,
			&ux.MultilineMessage{
				Lines: lines,
			},
		)
	}

	operations, err := display.resourceManager.GetDeploymentResourceOperations(ctx, display.deployment, queryStart)
	if err != nil {
		// Status display is best-effort activity.
		return err
	}

	newlyDeployedResources := []*armresources.DeploymentOperation{}
	newlyFailedResources := []*armresources.DeploymentOperation{}
	runningDeployments := []*armresources.DeploymentOperation{}

	for i := range operations {
		if operations[i].Properties.TargetResource != nil {
			resourceId := *operations[i].Properties.TargetResource.ResourceName

			if !display.displayedResources[resourceId] {
				switch *operations[i].Properties.ProvisioningState {
				case string(armresources.ProvisioningStateSucceeded):
					newlyDeployedResources = append(newlyDeployedResources, operations[i])
				case string(armresources.ProvisioningStateRunning):
					runningDeployments = append(runningDeployments, operations[i])
				case string(armresources.ProvisioningStateFailed):
					newlyFailedResources = append(newlyFailedResources, operations[i])
				}
			}
		}
	}

	sort.Slice(newlyDeployedResources, func(i int, j int) bool {
		return time.Time.Before(
			*newlyDeployedResources[i].Properties.Timestamp,
			*newlyDeployedResources[j].Properties.Timestamp,
		)
	})

	displayedResources := append(newlyDeployedResources, newlyFailedResources...)
	display.logNewlyCreatedResources(ctx, displayedResources, runningDeployments)
	return nil
}

func (display *ProvisioningProgressDisplay) logNewlyCreatedResources(
	ctx context.Context,
	resources []*armresources.DeploymentOperation,
	inProgressResources []*armresources.DeploymentOperation,
) {
	for _, resource := range resources {
		resourceTypeName := *resource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx,
			display.deployment.SubscriptionId(),
			*resource.Properties.TargetResource.ID,
			azapi.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			duration, err := convert.ParseDuration(*resource.Properties.Duration)
			if err != nil {
				duration = 0
			}

			display.console.MessageUxItem(
				ctx,
				&ux.DisplayedResource{
					Type:     resourceTypeDisplayName,
					Name:     *resource.Properties.TargetResource.ResourceName,
					State:    ux.DisplayedResourceState(*resource.Properties.ProvisioningState),
					Duration: duration.Truncate(time.Millisecond),
				},
			)
			resourceTypeName = resourceTypeDisplayName
		}

		log.Printf(
			"%s - %s %s: %s",
			resource.Properties.Timestamp.Local().Format("2006-01-02 15:04:05"),
			*resource.Properties.ProvisioningState,
			resourceTypeName,
			*resource.Properties.TargetResource.ResourceName)

		display.displayedResources[*resource.Properties.TargetResource.ResourceName] = true
	}
	// update progress
	inProgress := []string{}
	for _, inProgResource := range inProgressResources {
		resourceTypeName := *inProgResource.Properties.TargetResource.ResourceType
		resourceTypeDisplayName, err := display.resourceManager.GetResourceTypeDisplayName(
			ctx,
			display.deployment.SubscriptionId(),
			*inProgResource.Properties.TargetResource.ID,
			azapi.AzureResourceType(resourceTypeName),
		)

		if err != nil {
			// Dynamic resource type translation failed -- fallback to static translation
			resourceTypeDisplayName = azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(resourceTypeName))
		}

		// Don't log resource types for Azure resources that we do not have a translation of the resource type for.
		// This will be improved on in a future iteration.
		if resourceTypeDisplayName != "" {
			inProgress = append(inProgress, resourceTypeDisplayName)
		}
	}

	if !display.console.IsSpinnerInteractive() {
		// If non-interactive, we simply do not want to display spinner messages that ends up
		// being individual lines of messages on the console
		return
	}

	if len(inProgress) > 0 {
		display.console.ShowSpinner(ctx,
			fmt.Sprintf("Creating/Updating resources (%s)", strings.Join(inProgress, ", ")), input.Step)
	} else {
		display.console.ShowSpinner(ctx, "Creating/Updating resources", input.Step)
	}
}
