package transform

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/open-policy-agent/frameworks/constraint/pkg/apis/constraints"
	"github.com/open-policy-agent/frameworks/constraint/pkg/client/drivers/k8scel/schema"
	"github.com/open-policy-agent/frameworks/constraint/pkg/core/templates"
	admissionregistrationv1alpha1 "k8s.io/api/admissionregistration/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	rschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
)

func TestTemplateToPolicyDefinition(t *testing.T) {
	tests := []struct {
		name        string
		kind        string
		source      *schema.Source
		expectedErr error
		expected    *admissionregistrationv1alpha1.ValidatingAdmissionPolicy
	}{
		{
			name: "Valid Template",
			kind: "SomePolicy",
			source: &schema.Source{
				FailurePolicy: ptr.To[string]("Fail"),
				MatchConditions: []schema.MatchCondition{
					{
						Name:       "must_match_something",
						Expression: "true == true",
					},
				},
				Variables: []schema.Variable{
					{
						Name:       "my_variable",
						Expression: "true",
					},
				},
				Validations: []schema.Validation{
					{
						Expression:        "1 == 1",
						Message:           "some fallback message",
						MessageExpression: `"some CEL string"`,
					},
				},
			},
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-somepolicy",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicySpec{
					ParamKind: &admissionregistrationv1alpha1.ParamKind{
						APIVersion: "constraints.gatekeeper.sh/v1beta1",
						Kind:       "SomePolicy",
					},
					MatchConstraints: &admissionregistrationv1alpha1.MatchResources{
						ResourceRules: []admissionregistrationv1alpha1.NamedRuleWithOperations{
							{
								RuleWithOperations: admissionregistrationv1alpha1.RuleWithOperations{
									Operations: []admissionregistrationv1alpha1.OperationType{admissionregistrationv1alpha1.OperationAll},
									Rule:       admissionregistrationv1alpha1.Rule{APIGroups: []string{"*"}, APIVersions: []string{"*"}, Resources: []string{"*"}},
								},
							},
						},
					},
					MatchConditions: []admissionregistrationv1alpha1.MatchCondition{
						{
							Name:       "must_match_something",
							Expression: "true == true",
						},
						{
							Name:       "gatekeeper_internal_match_excluded_namespaces",
							Expression: matchExcludedNamespacesGlob,
						},
						{
							Name:       "gatekeeper_internal_match_namespaces",
							Expression: matchNamespacesGlob,
						},
						{
							Name:       "gatekeeper_internal_match_name",
							Expression: matchNameGlob,
						},
						{
							Name:       "gatekeeper_internal_match_kinds",
							Expression: matchKinds,
						},
					},
					Validations: []admissionregistrationv1alpha1.Validation{
						{
							Expression:        "1 == 1",
							Message:           "some fallback message",
							MessageExpression: `"some CEL string"`,
						},
					},
					FailurePolicy: ptr.To[admissionregistrationv1alpha1.FailurePolicyType](admissionregistrationv1alpha1.Fail),
					Variables: []admissionregistrationv1alpha1.Variable{
						{
							Name:       "my_variable",
							Expression: "true",
						},
						{
							Name:       schema.ParamsName,
							Expression: "!has(params.spec) ? null : !has(params.spec.parameters) ? null: params.spec.parameters",
						},
					},
				},
			},
		},
		{
			name: "Invalid Match Condition",
			kind: "SomePolicy",
			source: &schema.Source{
				FailurePolicy: ptr.To[string]("Fail"),
				MatchConditions: []schema.MatchCondition{
					{
						Name:       "gatekeeper_internal_match_something",
						Expression: "true == true",
					},
				},
				Variables: []schema.Variable{
					{
						Name:       "my_variable",
						Expression: "true",
					},
				},
				Validations: []schema.Validation{
					{
						Expression:        "1 == 1",
						Message:           "some fallback message",
						MessageExpression: `"some CEL string"`,
					},
				},
			},
			expectedErr: schema.ErrBadMatchCondition,
		},
		{
			name: "Invalid Variable",
			kind: "SomePolicy",
			source: &schema.Source{
				FailurePolicy: ptr.To[string]("Fail"),
				MatchConditions: []schema.MatchCondition{
					{
						Name:       "match_something",
						Expression: "true == true",
					},
				},
				Variables: []schema.Variable{
					{
						Name:       "gatekeeper_internal_my_variable",
						Expression: "true",
					},
				},
				Validations: []schema.Validation{
					{
						Expression:        "1 == 1",
						Message:           "some fallback message",
						MessageExpression: `"some CEL string"`,
					},
				},
			},
			expectedErr: schema.ErrBadVariable,
		},
		{
			name: "No Clobbering Params",
			kind: "SomePolicy",
			source: &schema.Source{
				FailurePolicy: ptr.To[string]("Fail"),
				MatchConditions: []schema.MatchCondition{
					{
						Name:       "match_something",
						Expression: "true == true",
					},
				},
				Variables: []schema.Variable{
					{
						Name:       "params",
						Expression: "true",
					},
				},
				Validations: []schema.Validation{
					{
						Expression:        "1 == 1",
						Message:           "some fallback message",
						MessageExpression: `"some CEL string"`,
					},
				},
			},
			expectedErr: schema.ErrBadVariable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawSrc := test.source.MustToUnstructured()

			template := &templates.ConstraintTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Name: strings.ToLower(test.kind),
				},
				Spec: templates.ConstraintTemplateSpec{
					CRD: templates.CRD{
						Spec: templates.CRDSpec{
							Names: templates.Names{
								Kind: test.kind,
							},
						},
					},
					Targets: []templates.Target{
						{
							Code: []templates.Code{
								{
									Engine: schema.Name,
									Source: &templates.Anything{
										Value: rawSrc,
									},
								},
							},
						},
					},
				},
			}

			obj, err := TemplateToPolicyDefinition(template)
			if !errors.Is(err, test.expectedErr) {
				t.Errorf("unexpected error. got %v; wanted %v", err, test.expectedErr)
			}
			if !reflect.DeepEqual(obj, test.expected) {
				t.Errorf("got %+v\n\nwant %+v", *obj, *test.expected)
			}
		})
	}
}

func newTestConstraint(enforcementAction string, namespaceSelector, labelSelector *metav1.LabelSelector) *unstructured.Unstructured {
	constraint := &unstructured.Unstructured{}
	constraint.SetGroupVersionKind(rschema.GroupVersionKind{Group: constraints.Group, Version: "v1beta1", Kind: "FooTemplate"})
	constraint.SetName("foo-name")
	if namespaceSelector != nil {
		nss, err := runtime.DefaultUnstructuredConverter.ToUnstructured(namespaceSelector)
		if err != nil {
			panic(fmt.Errorf("%w: could not convert namespace selector", err))
		}
		if err := unstructured.SetNestedMap(constraint.Object, nss, "spec", "match", "namespaceSelector"); err != nil {
			panic(fmt.Errorf("%w: could not set namespace selector", err))
		}
	}
	if labelSelector != nil {
		ls, err := runtime.DefaultUnstructuredConverter.ToUnstructured(labelSelector)
		if err != nil {
			panic(fmt.Errorf("%w: could not convert label selector", err))
		}
		if err := unstructured.SetNestedMap(constraint.Object, ls, "spec", "match", "labelSelector"); err != nil {
			panic(fmt.Errorf("%w: could not set label selector", err))
		}
	}
	if enforcementAction != "" {
		if err := unstructured.SetNestedField(constraint.Object, enforcementAction, "spec", "enforcementAction"); err != nil {
			panic(fmt.Errorf("%w: could not set enforcement action", err))
		}
	}
	return constraint
}

func TestConstraintToBinding(t *testing.T) {
	tests := []struct {
		name        string
		constraint  *unstructured.Unstructured
		expectedErr error
		expected    *admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding
	}{
		{
			name:       "empty constraint",
			constraint: newTestConstraint("", nil, nil),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources:    &admissionregistrationv1alpha1.MatchResources{},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Deny},
				},
			},
		},
		{
			name:       "with object selector",
			constraint: newTestConstraint("", nil, &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}}),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources: &admissionregistrationv1alpha1.MatchResources{
						ObjectSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}},
					},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Deny},
				},
			},
		},
		{
			name:       "with namespace selector",
			constraint: newTestConstraint("", &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}}, nil),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources: &admissionregistrationv1alpha1.MatchResources{
						NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}},
					},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Deny},
				},
			},
		},
		{
			name:       "with both selectors",
			constraint: newTestConstraint("", &metav1.LabelSelector{MatchLabels: map[string]string{"matchNS": "yes"}}, &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}}),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources: &admissionregistrationv1alpha1.MatchResources{
						ObjectSelector:    &metav1.LabelSelector{MatchLabels: map[string]string{"match": "yes"}},
						NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"matchNS": "yes"}},
					},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Deny},
				},
			},
		},
		{
			name:       "with explicit deny",
			constraint: newTestConstraint("deny", nil, nil),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources:    &admissionregistrationv1alpha1.MatchResources{},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Deny},
				},
			},
		},
		{
			name:       "with warn",
			constraint: newTestConstraint("warn", nil, nil),
			expected: &admissionregistrationv1alpha1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gatekeeper-foo-name",
				},
				Spec: admissionregistrationv1alpha1.ValidatingAdmissionPolicyBindingSpec{
					PolicyName: "gatekeeper-footemplate",
					ParamRef: &admissionregistrationv1alpha1.ParamRef{
						Name:                    "foo-name",
						ParameterNotFoundAction: ptr.To[admissionregistrationv1alpha1.ParameterNotFoundActionType](admissionregistrationv1alpha1.AllowAction),
					},
					MatchResources:    &admissionregistrationv1alpha1.MatchResources{},
					ValidationActions: []admissionregistrationv1alpha1.ValidationAction{admissionregistrationv1alpha1.Warn},
				},
			},
		},
		{
			name:        "unrecognized enforcement action",
			constraint:  newTestConstraint("magicunicorns", nil, nil),
			expected:    nil,
			expectedErr: ErrBadEnforcementAction,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			binding, err := ConstraintToBinding(test.constraint)
			if !errors.Is(err, test.expectedErr) {
				t.Errorf("unexpected error. got %v; wanted %v", err, test.expectedErr)
			}
			if !reflect.DeepEqual(binding, test.expected) {
				t.Errorf("got %+v\n\nwant %+v", *binding, *test.expected)
			}
		})
	}
}
