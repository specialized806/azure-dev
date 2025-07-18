// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

func TestPromptForParameter(t *testing.T) {
	t.Parallel()

	for _, cc := range []struct {
		name      string
		paramType string
		provided  any
		expected  any
	}{
		{"string", "string", "value", "value"},
		{"emptyString", "string", "", ""},
		{"int", "int", "1", 1},
		{"intNegative", "int", "-1", -1},
		{"boolTrue", "bool", 0, false},
		{"boolFalse", "bool", 1, true},
		{"arrayParam", "array", `["hello", "world"]`, []any{"hello", "world"}},
		{"objectParam", "object", `{"hello": "world"}`, map[string]any{"hello": "world"}},
		{"secureObject", "secureObject", `{"hello": "world"}`, map[string]any{"hello": "world"}},
		{"secureString", "secureString", "value", "value"},
	} {
		tc := cc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(context.Background())
			prepareBicepMocks(mockContext)

			p := createBicepProvider(t, mockContext)

			if _, ok := tc.provided.(int); ok {
				mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
				}).Respond(tc.provided)
			} else {
				mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
				}).Respond(tc.provided)
			}

			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return true
			}).Respond(tc.provided)

			value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
				Type: tc.paramType,
			}, nil)

			require.NoError(t, err)
			require.Equal(t, tc.expected, value)
		})
	}
}

func TestPromptForParameterValidation(t *testing.T) {
	t.Parallel()

	for _, cc := range []struct {
		name     string
		param    azure.ArmTemplateParameterDefinition
		provided []string
		expected any
		messages []string
	}{
		{
			name: "minValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MinValue: to.Ptr(1),
			},
			provided: []string{"0", "1"},
			expected: 1,
			messages: []string{"at least '1'"},
		},
		{
			name: "maxValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MaxValue: to.Ptr(10),
			},
			provided: []string{"11", "10"},
			expected: 10,
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeValue",
			param: azure.ArmTemplateParameterDefinition{
				Type:     "int",
				MinValue: to.Ptr(1),
				MaxValue: to.Ptr(10),
			},
			provided: []string{"0", "11", "5"},
			expected: 5,
			messages: []string{"at least '1'", "at most '10'"},
		},
		{
			name: "minLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: to.Ptr(1),
			},
			provided: []string{"", "ok"},
			expected: "ok",
			messages: []string{"at least '1'"},
		},
		{
			name: "maxLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MaxLength: to.Ptr(10),
			},
			provided: []string{"this is a very long string and will be rejected", "ok"},
			expected: "ok",
			messages: []string{"at most '10'"},
		},
		{
			name: "rangeLength",
			param: azure.ArmTemplateParameterDefinition{
				Type:      "string",
				MinLength: to.Ptr(1),
				MaxLength: to.Ptr(10),
			},
			provided: []string{"this is a very long string and will be rejected", "", "ok"},
			expected: "ok",
			messages: []string{"at least '1'", "at most '10'"},
		},
		{
			name: "badInt",
			param: azure.ArmTemplateParameterDefinition{
				Type: "int",
			},
			provided: []string{"bad", "100"},
			expected: 100,
			messages: []string{"failed to convert 'bad' to an integer"},
		},
		{
			name: "badJsonObject",
			param: azure.ArmTemplateParameterDefinition{
				Type: "object",
			},
			provided: []string{"[]", "{}"},
			expected: make(map[string]any),
			messages: []string{"failed to parse value as a JSON object"},
		},
		{
			name: "badJsonArray",
			param: azure.ArmTemplateParameterDefinition{
				Type: "array",
			},
			provided: []string{"{}", "[]"},
			expected: []any{},
			messages: []string{"failed to parse value as a JSON array"},
		},
	} {
		tc := cc

		t.Run(tc.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			prepareBicepMocks(mockContext)

			p := createBicepProvider(t, mockContext)

			mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
				return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
			}).RespondFn(func(options input.ConsoleOptions) (any, error) {
				ret := tc.provided[0]
				tc.provided = tc.provided[1:]
				return ret, nil
			})

			value, err := p.promptForParameter(*mockContext.Context, "testParam", tc.param, nil)
			require.NoError(t, err)
			require.Equal(t, tc.expected, value)

			outputLog := mockContext.Console.Output()
			for _, msg := range tc.messages {
				match := false
				for _, line := range outputLog {
					match = match || strings.Contains(line, msg)
				}
				require.True(t, match, "the output log: %#v should have contained '%s' but did not", outputLog, msg)
			}
		})
	}
}

func TestPromptForParameterAllowedValues(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 3, len(options.Options))

		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "good", value)

	value, err = p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "int",
		AllowedValues: to.Ptr([]any{10, 20, 30}),
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 20, value)
}

func TestPromptForParametersLocation(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	prepareBicepMocks(mockContext)

	env := environment.New("test")
	accountManager := &mockaccount.MockAccountManager{
		Subscriptions: []account.Subscription{
			{
				Id:   "00000000-0000-0000-0000-000000000000",
				Name: "test",
			},
		},
		Locations: []account.Location{
			{
				Name:                "eastus",
				DisplayName:         "East US",
				RegionalDisplayName: "(US) East US",
			},
			{
				Name:                "eastus2",
				DisplayName:         "East US 2",
				RegionalDisplayName: "(US) East US 2",
			},
			{
				Name:                "westus",
				DisplayName:         "West US",
				RegionalDisplayName: "(US) West US",
			},
		},
	}

	p := createBicepProvider(t, mockContext)
	p.prompters = prompt.NewDefaultPrompter(
		env,
		mockContext.Console,
		accountManager,
		p.resourceService,
		cloud.AzurePublic(),
	)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "'unfilteredLocation")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Len(t, options.Options, 3)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "unfilteredLocation", azure.ArmTemplateParameterDefinition{
		Type: "string",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"type": "location"}`),
		},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "eastus2", value)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "filteredLocation")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Len(t, options.Options, 1)
		return 0, nil
	})

	value, err = p.promptForParameter(*mockContext.Context, "filteredLocation", azure.ArmTemplateParameterDefinition{
		Type: "string",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"type": "location"}`),
		},
		AllowedValues: &[]any{"westus"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "westus", value)
}

type mockCurrentPrincipal struct{}

func (m *mockCurrentPrincipal) CurrentPrincipalId(_ context.Context) (string, error) {
	return "11111111-1111-1111-1111-111111111111", nil
}

func (m *mockCurrentPrincipal) CurrentPrincipalType(_ context.Context) (provisioning.PrincipalType, error) {
	return provisioning.UserType, nil
}

func TestPromptForParameterOverrideDefault(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 3, len(options.Options))
		require.Equal(t, "good", options.DefaultValue)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "good"}`),
		},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "good", value)
}

func TestPromptForParameterOverrideDefaultError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{"three", "good", "choices"}),
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "other"}`),
		},
	}, nil)

	require.Error(t, err)
}

func TestPromptForParameterEmptyAllowedValuesError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type:          "string",
		AllowedValues: to.Ptr([]any{}),
	}, nil)

	require.Error(t, err)
}

func TestPromptForParameterBoolDefaultType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 2, len(options.Options))
		require.Equal(t, "True", options.DefaultValue)
		return 1, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "bool",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": true}`)},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, true, value)
}

func TestPromptForParameterBoolDefaultStringType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		require.Equal(t, 2, len(options.Options))
		require.Equal(t, "False", options.DefaultValue)
		return 0, nil
	})

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "bool",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "false"}`)},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, false, value)
}

func TestPromptForParameterNumberDefaultType(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).Respond("33")

	value, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "int",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": 33}`)},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, 33, value)
}

func TestPromptForParameterNumberDefaultStringTypeError(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())

	prepareBicepMocks(mockContext)

	p := createBicepProvider(t, mockContext)

	mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return strings.Contains(options.Message, "for the 'testParam' infrastructure parameter")
	}).Respond("33")

	_, err := p.promptForParameter(*mockContext.Context, "testParam", azure.ArmTemplateParameterDefinition{
		Type: "int",
		Metadata: map[string]json.RawMessage{
			"azd": json.RawMessage(`{"default": "33"}`)},
	}, nil)

	require.Error(t, err)
}
