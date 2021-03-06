package api_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/gofuzz"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/meta"
	"k8s.io/kubernetes/pkg/api/testapi"
	apitesting "k8s.io/kubernetes/pkg/api/testing"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/api/validation"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/runtime/serializer/protobuf"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util/diff"
	"k8s.io/kubernetes/pkg/util/intstr"
	"k8s.io/kubernetes/pkg/util/sets"

	osapi "github.com/openshift/origin/pkg/api"
	_ "github.com/openshift/origin/pkg/api/latest"
	"github.com/openshift/origin/pkg/api/v1"
	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	build "github.com/openshift/origin/pkg/build/api"
	deploy "github.com/openshift/origin/pkg/deploy/api"
	image "github.com/openshift/origin/pkg/image/api"
	"github.com/openshift/origin/pkg/image/api/docker10"
	"github.com/openshift/origin/pkg/image/api/dockerpre012"
	oauthapi "github.com/openshift/origin/pkg/oauth/api"
	quotaapi "github.com/openshift/origin/pkg/quota/api"
	quotaapiv1 "github.com/openshift/origin/pkg/quota/api/v1"
	route "github.com/openshift/origin/pkg/route/api"
	securityapi "github.com/openshift/origin/pkg/security/api"
	template "github.com/openshift/origin/pkg/template/api"
	uservalidation "github.com/openshift/origin/pkg/user/api/validation"

	// install all APIs
	_ "github.com/openshift/origin/pkg/api/install"
	_ "github.com/openshift/origin/pkg/quota/api/install"
	_ "k8s.io/kubernetes/pkg/api/install"
)

var codecsToTest = []func(version unversioned.GroupVersion, item runtime.Object) (runtime.Codec, error){
	func(version unversioned.GroupVersion, item runtime.Object) (runtime.Codec, error) {
		return kapi.Codecs.LegacyCodec(version), nil
	},
	func(version unversioned.GroupVersion, item runtime.Object) (runtime.Codec, error) {
		s := protobuf.NewSerializer(kapi.Scheme, kapi.Scheme, "application/arbitrary.content.type")
		return kapi.Codecs.CodecForVersions(s, s, testapi.ExternalGroupVersions(), nil), nil
	},
}

func fuzzInternalObject(t *testing.T, forVersion unversioned.GroupVersion, item runtime.Object, seed int64) runtime.Object {
	f := apitesting.FuzzerFor(t, forVersion, rand.NewSource(seed))
	f.Funcs(
		// Roles and RoleBindings maps are never nil
		func(j *authorizationapi.Policy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.Roles != nil {
				j.Roles = make(map[string]*authorizationapi.Role)
			}
			for k, v := range j.Roles {
				if v == nil {
					delete(j.Roles, k)
				}
			}
		},
		func(j *authorizationapi.PolicyBinding, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.RoleBindings == nil {
				j.RoleBindings = make(map[string]*authorizationapi.RoleBinding)
			}
			for k, v := range j.RoleBindings {
				if v == nil {
					delete(j.RoleBindings, k)
				}
			}
		},
		func(j *authorizationapi.ClusterPolicy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.Roles == nil {
				j.Roles = make(map[string]*authorizationapi.ClusterRole)
			}
			for k, v := range j.Roles {
				if v == nil {
					delete(j.Roles, k)
				}
			}
		},
		func(j *authorizationapi.ClusterPolicyBinding, c fuzz.Continue) {
			j.RoleBindings = make(map[string]*authorizationapi.ClusterRoleBinding)
		},
		func(j *authorizationapi.RoleBinding, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			for i := range j.Subjects {
				kinds := []string{authorizationapi.UserKind, authorizationapi.SystemUserKind, authorizationapi.GroupKind, authorizationapi.SystemGroupKind, authorizationapi.ServiceAccountKind}
				j.Subjects[i].Kind = kinds[c.Intn(len(kinds))]
				switch j.Subjects[i].Kind {
				case authorizationapi.UserKind:
					j.Subjects[i].Namespace = ""
					if len(uservalidation.ValidateUserName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("validusername%d", i)
					}

				case authorizationapi.GroupKind:
					j.Subjects[i].Namespace = ""
					if len(uservalidation.ValidateGroupName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("validgroupname%d", i)
					}

				case authorizationapi.ServiceAccountKind:
					if len(validation.ValidateNamespaceName(j.Subjects[i].Namespace, false)) != 0 {
						j.Subjects[i].Namespace = fmt.Sprintf("sanamespacehere%d", i)
					}
					if len(validation.ValidateServiceAccountName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("sanamehere%d", i)
					}

				case authorizationapi.SystemUserKind, authorizationapi.SystemGroupKind:
					j.Subjects[i].Namespace = ""
					j.Subjects[i].Name = ":" + j.Subjects[i].Name

				}

				j.Subjects[i].UID = types.UID("")
				j.Subjects[i].APIVersion = ""
				j.Subjects[i].ResourceVersion = ""
				j.Subjects[i].FieldPath = ""
			}
		},
		func(j *authorizationapi.PolicyRule, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			// if no groups are found, then we assume "".  This matches defaulting
			if len(j.APIGroups) == 0 {
				j.APIGroups = []string{""}
			}
			switch c.Intn(3) {
			case 0:
				j.AttributeRestrictions = &authorizationapi.IsPersonalSubjectAccessReview{}
			case 1:
				j.AttributeRestrictions = &runtime.Unknown{TypeMeta: runtime.TypeMeta{Kind: "Type", APIVersion: "other"}, ContentType: "application/json", Raw: []byte(`{"apiVersion":"other","kind":"Type"}`)}
			default:
				j.AttributeRestrictions = nil
			}
		},
		func(j *authorizationapi.ClusterRoleBinding, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			for i := range j.Subjects {
				kinds := []string{authorizationapi.UserKind, authorizationapi.SystemUserKind, authorizationapi.GroupKind, authorizationapi.SystemGroupKind, authorizationapi.ServiceAccountKind}
				j.Subjects[i].Kind = kinds[c.Intn(len(kinds))]
				switch j.Subjects[i].Kind {
				case authorizationapi.UserKind:
					j.Subjects[i].Namespace = ""
					if len(uservalidation.ValidateUserName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("validusername%d", i)
					}

				case authorizationapi.GroupKind:
					j.Subjects[i].Namespace = ""
					if len(uservalidation.ValidateGroupName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("validgroupname%d", i)
					}

				case authorizationapi.ServiceAccountKind:
					if len(validation.ValidateNamespaceName(j.Subjects[i].Namespace, false)) != 0 {
						j.Subjects[i].Namespace = fmt.Sprintf("sanamespacehere%d", i)
					}
					if len(validation.ValidateServiceAccountName(j.Subjects[i].Name, false)) != 0 {
						j.Subjects[i].Name = fmt.Sprintf("sanamehere%d", i)
					}

				case authorizationapi.SystemUserKind, authorizationapi.SystemGroupKind:
					j.Subjects[i].Namespace = ""
					j.Subjects[i].Name = ":" + j.Subjects[i].Name

				}

				j.Subjects[i].UID = types.UID("")
				j.Subjects[i].APIVersion = ""
				j.Subjects[i].ResourceVersion = ""
				j.Subjects[i].FieldPath = ""
			}
		},
		func(j *template.Template, c fuzz.Continue) {
			c.Fuzz(&j.ObjectMeta)
			c.Fuzz(&j.Parameters)
			// TODO: replace with structured type definition
			j.Objects = []runtime.Object{}
		},
		func(j *image.Image, c fuzz.Continue) {
			c.Fuzz(&j.ObjectMeta)
			c.Fuzz(&j.DockerImageMetadata)
			c.Fuzz(&j.Signatures)
			j.DockerImageMetadata.APIVersion = ""
			j.DockerImageMetadata.Kind = ""
			j.DockerImageMetadataVersion = []string{"pre012", "1.0"}[c.Rand.Intn(2)]
			j.DockerImageReference = c.RandString()
		},
		func(j *image.ImageSignature, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Conditions = nil
			j.ImageIdentity = ""
			j.SignedClaims = nil
			j.Created = nil
			j.IssuedBy = nil
			j.IssuedTo = nil
		},
		func(j *image.ImageStreamMapping, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.DockerImageRepository = ""
		},
		func(j *image.ImageImportSpec, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.To == nil {
				// To is defaulted to be not nil
				j.To = &kapi.LocalObjectReference{}
			}
		},
		func(j *image.ImageStreamImage, c fuzz.Continue) {
			c.Fuzz(&j.Image)
			// because we de-embedded Image from ImageStreamImage, in order to round trip
			// successfully, the ImageStreamImage's ObjectMeta must match the Image's.
			j.ObjectMeta = j.Image.ObjectMeta
		},
		func(j *image.ImageStreamSpec, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			// if the generated fuzz value has a tag or image id, strip it
			if strings.ContainsAny(j.DockerImageRepository, ":@") {
				j.DockerImageRepository = ""
			}
			if j.Tags == nil {
				j.Tags = make(map[string]image.TagReference)
			}
			for k, v := range j.Tags {
				v.Name = k
				j.Tags[k] = v
			}
		},
		func(j *image.ImageStreamStatus, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			// if the generated fuzz value has a tag or image id, strip it
			if strings.ContainsAny(j.DockerImageRepository, ":@") {
				j.DockerImageRepository = ""
			}
		},
		func(j *image.ImageStreamTag, c fuzz.Continue) {
			c.Fuzz(&j.Image)
			// because we de-embedded Image from ImageStreamTag, in order to round trip
			// successfully, the ImageStreamTag's ObjectMeta must match the Image's.
			j.ObjectMeta = j.Image.ObjectMeta
		},
		func(j *image.TagReference, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.From != nil {
				specs := []string{"", "ImageStreamTag", "ImageStreamImage"}
				j.From.Kind = specs[c.Intn(len(specs))]
			}
		},
		func(j *build.BuildConfigSpec, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.RunPolicy = build.BuildRunPolicySerial
		},
		func(j *build.SourceBuildStrategy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.From.Kind = "ImageStreamTag"
			j.From.Name = "image:tag"
			j.From.APIVersion = ""
			j.From.ResourceVersion = ""
			j.From.FieldPath = ""
		},
		func(j *build.CustomBuildStrategy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.From.Kind = "ImageStreamTag"
			j.From.Name = "image:tag"
			j.From.APIVersion = ""
			j.From.ResourceVersion = ""
			j.From.FieldPath = ""
		},
		func(j *build.DockerBuildStrategy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.From.Kind = "ImageStreamTag"
			j.From.Name = "image:tag"
			j.From.APIVersion = ""
			j.From.ResourceVersion = ""
			j.From.FieldPath = ""
		},
		func(j *build.BuildOutput, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if j.To != nil && (len(j.To.Kind) == 0 || j.To.Kind == "ImageStream") {
				j.To.Kind = "ImageStreamTag"
			}
			if j.To != nil && strings.Contains(j.To.Name, ":") {
				j.To.Name = strings.Replace(j.To.Name, ":", "-", -1)
			}
		},
		func(j *route.RouteTargetReference, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Kind = "Service"
			j.Weight = new(int32)
			*j.Weight = 100
		},
		func(j *route.TLSConfig, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if len(j.Termination) == 0 && len(j.DestinationCACertificate) == 0 {
				j.Termination = route.TLSTerminationEdge
			}
		},
		func(j *deploy.DeploymentConfig, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Spec.Triggers = []deploy.DeploymentTriggerPolicy{{Type: deploy.DeploymentTriggerOnConfigChange}}
			if j.Spec.Template != nil && len(j.Spec.Template.Spec.Containers) == 1 {
				containerName := j.Spec.Template.Spec.Containers[0].Name
				if p := j.Spec.Strategy.RecreateParams; p != nil {
					defaultHookContainerName(p.Pre, containerName)
					defaultHookContainerName(p.Mid, containerName)
					defaultHookContainerName(p.Post, containerName)
				}
				if p := j.Spec.Strategy.RollingParams; p != nil {
					defaultHookContainerName(p.Pre, containerName)
					defaultHookContainerName(p.Post, containerName)
				}
			}
		},
		func(j *deploy.DeploymentStrategy, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.RecreateParams, j.RollingParams, j.CustomParams = nil, nil, nil
			strategyTypes := []deploy.DeploymentStrategyType{deploy.DeploymentStrategyTypeRecreate, deploy.DeploymentStrategyTypeRolling, deploy.DeploymentStrategyTypeCustom}
			j.Type = strategyTypes[c.Rand.Intn(len(strategyTypes))]
			switch j.Type {
			case deploy.DeploymentStrategyTypeRecreate:
				params := &deploy.RecreateDeploymentStrategyParams{}
				c.Fuzz(params)
				if params.TimeoutSeconds == nil {
					s := int64(120)
					params.TimeoutSeconds = &s
				}
				j.RecreateParams = params
			case deploy.DeploymentStrategyTypeRolling:
				params := &deploy.RollingDeploymentStrategyParams{}
				randInt64 := func() *int64 {
					p := int64(c.RandUint64())
					return &p
				}
				params.TimeoutSeconds = randInt64()
				params.IntervalSeconds = randInt64()
				params.UpdatePeriodSeconds = randInt64()

				policyTypes := []deploy.LifecycleHookFailurePolicy{
					deploy.LifecycleHookFailurePolicyRetry,
					deploy.LifecycleHookFailurePolicyAbort,
					deploy.LifecycleHookFailurePolicyIgnore,
				}
				if c.RandBool() {
					params.Pre = &deploy.LifecycleHook{
						FailurePolicy: policyTypes[c.Rand.Intn(len(policyTypes))],
						ExecNewPod: &deploy.ExecNewPodHook{
							ContainerName: c.RandString(),
						},
					}
				}
				if c.RandBool() {
					params.Post = &deploy.LifecycleHook{
						FailurePolicy: policyTypes[c.Rand.Intn(len(policyTypes))],
						ExecNewPod: &deploy.ExecNewPodHook{
							ContainerName: c.RandString(),
						},
					}
				}
				if c.RandBool() {
					params.MaxUnavailable = intstr.FromInt(int(c.RandUint64()))
					params.MaxSurge = intstr.FromInt(int(c.RandUint64()))
				} else {
					params.MaxSurge = intstr.FromString(fmt.Sprintf("%d%%", c.RandUint64()))
					params.MaxUnavailable = intstr.FromString(fmt.Sprintf("%d%%", c.RandUint64()))
				}
				j.RollingParams = params
			}
		},
		func(j *deploy.DeploymentCauseImageTrigger, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			specs := []string{"", "a/b", "a/b/c", "a:5000/b/c", "a/b", "a/b"}
			tags := []string{"stuff", "other"}
			j.From.Name = specs[c.Intn(len(specs))]
			if len(j.From.Name) > 0 {
				j.From.Name = image.JoinImageStreamTag(j.From.Name, tags[c.Intn(len(tags))])
			}
		},
		func(j *deploy.DeploymentTriggerImageChangeParams, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			specs := []string{"a/b", "a/b/c", "a:5000/b/c", "a/b:latest", "a/b@test"}
			j.From.Kind = "DockerImage"
			j.From.Name = specs[c.Intn(len(specs))]
		},

		// TODO: uncomment when round tripping for init containers is available (the annotation is
		// not supported on security context review for now)
		func(j *securityapi.PodSecurityPolicyReview, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Spec.Template.Spec.InitContainers = nil
			for i := range j.Status.AllowedServiceAccounts {
				j.Status.AllowedServiceAccounts[i].Template.Spec.InitContainers = nil
			}
		},
		func(j *securityapi.PodSecurityPolicySelfSubjectReview, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Spec.Template.Spec.InitContainers = nil
			j.Status.Template.Spec.InitContainers = nil
		},
		func(j *securityapi.PodSecurityPolicySubjectReview, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			j.Spec.Template.Spec.InitContainers = nil
			j.Status.Template.Spec.InitContainers = nil
		},
		func(j *oauthapi.OAuthAuthorizeToken, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if len(j.CodeChallenge) > 0 && len(j.CodeChallengeMethod) == 0 {
				j.CodeChallengeMethod = "plain"
			}
		},
		func(j *oauthapi.OAuthClientAuthorization, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if len(j.Scopes) == 0 {
				j.Scopes = append(j.Scopes, "user:full")
			}
		},
		func(j *route.RouteSpec, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if len(j.WildcardPolicy) == 0 {
				j.WildcardPolicy = route.WildcardPolicyNone
			}
		},
		func(j *route.RouteIngress, c fuzz.Continue) {
			c.FuzzNoCustom(j)
			if len(j.WildcardPolicy) == 0 {
				j.WildcardPolicy = route.WildcardPolicyNone
			}
		},

		func(j *runtime.Object, c fuzz.Continue) {
			// runtime.EmbeddedObject causes a panic inside of fuzz because runtime.Object isn't handled.
		},
		func(t *time.Time, c fuzz.Continue) {
			// This is necessary because the standard fuzzed time.Time object is
			// completely nil, but when JSON unmarshals dates it fills in the
			// unexported loc field with the time.UTC object, resulting in
			// reflect.DeepEqual returning false in the round trip tests. We solve it
			// by using a date that will be identical to the one JSON unmarshals.
			*t = time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC)
		},
		func(u64 *uint64, c fuzz.Continue) {
			// TODO: uint64's are NOT handled right.
			*u64 = c.RandUint64() >> 8
		},
	)

	f.Fuzz(item)

	j, err := meta.TypeAccessor(item)
	if err != nil {
		t.Fatalf("Unexpected error %v for %#v", err, item)
	}
	j.SetKind("")
	j.SetAPIVersion("")

	return item
}

func defaultHookContainerName(hook *deploy.LifecycleHook, containerName string) {
	if hook == nil {
		return
	}
	for i := range hook.TagImages {
		if len(hook.TagImages[i].ContainerName) == 0 {
			hook.TagImages[i].ContainerName = containerName
		}
	}
	if hook.ExecNewPod != nil {
		if len(hook.ExecNewPod.ContainerName) == 0 {
			hook.ExecNewPod.ContainerName = containerName
		}
	}
}

func roundTripWithAllCodecs(t *testing.T, version unversioned.GroupVersion, item runtime.Object) {
	var codecs []runtime.Codec
	for _, fn := range codecsToTest {
		codec, err := fn(version, item)
		if err != nil {
			t.Errorf("unable to get codec: %v", err)
			return
		}
		codecs = append(codecs, codec)
	}
	for _, codec := range codecs {
		roundTrip(t, codec, item)
	}
}

func roundTrip(t *testing.T, codec runtime.Codec, originalItem runtime.Object) {
	// Make a copy of the originalItem to give to conversion functions
	// This lets us know if conversion messed with the input object
	deepCopy, err := kapi.Scheme.DeepCopy(originalItem)
	if err != nil {
		t.Errorf("Could not copy object: %v", err)
		return
	}
	item := deepCopy.(runtime.Object)

	name := reflect.TypeOf(item).Elem().Name()
	data, err := runtime.Encode(codec, item)
	if err != nil {
		if runtime.IsNotRegisteredError(err) {
			t.Logf("%v skipped: not registered: %v", name, err)
			return
		}
		t.Errorf("%v: %v (%#v)", name, err, item)
		return
	}

	obj2, err := runtime.Decode(codec, data)
	if err != nil {
		t.Errorf("0: %v: %v\nCodec: %v\nData: %s\nSource: %#v", name, err, codec, string(data), originalItem)
		return
	}
	if reflect.TypeOf(item) != reflect.TypeOf(obj2) {
		obj2conv := reflect.New(reflect.TypeOf(item).Elem()).Interface().(runtime.Object)
		if err := kapi.Scheme.Convert(obj2, obj2conv, nil); err != nil {
			t.Errorf("0X: no conversion from %v to %v: %v", reflect.TypeOf(item), reflect.TypeOf(obj2), err)
			return
		}
		obj2 = obj2conv
	}

	if !kapi.Semantic.DeepEqual(originalItem, obj2) {
		t.Errorf("1: %v: diff: %v\nCodec: %v\nData: %s", name, diff.ObjectReflectDiff(originalItem, obj2), codec, dataToString(data))
		return
	}

	obj3 := reflect.New(reflect.TypeOf(item).Elem()).Interface().(runtime.Object)
	if err := runtime.DecodeInto(codec, data, obj3); err != nil {
		t.Errorf("2: %v: %v", name, err)
		return
	}
	if !kapi.Semantic.DeepEqual(originalItem, obj3) {
		t.Errorf("3: %v: diff: %v\nCodec: %v", name, diff.ObjectReflectDiff(originalItem, obj3), codec)
		return
	}
}

func dataToString(s []byte) string {
	if bytes.HasPrefix(s, []byte("k8s")) {
		return "\n" + hex.Dump(s)
	}
	return string(s)
}

// skipStandardVersions is a map of Kind to a list of API versions to test with.
var skipStandardVersions = map[string][]unversioned.GroupVersion{
	// The API versions here are to test our object that serializes from/into
	// docker's registry API.
	"DockerImage": {dockerpre012.SchemeGroupVersion, docker10.SchemeGroupVersion},
}

const fuzzIters = 20

// For debugging problems
func TestSpecificKind(t *testing.T) {
	kapi.Scheme.Log(t)
	defer kapi.Scheme.Log(nil)

	kind := "ClusterRole"
	item, err := kapi.Scheme.New(osapi.SchemeGroupVersion.WithKind(kind))
	if err != nil {
		t.Fatalf("Couldn't make a %v? %v", kind, err)
	}
	codec, err := codecsToTest[1](v1.SchemeGroupVersion, nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(2703387474910584091)
	for i := 0; i < fuzzIters; i++ {
		//t.Logf(`About to test %v with "v1"`, kind)
		fuzzInternalObject(t, v1.SchemeGroupVersion, item, seed)
		roundTrip(t, codec, item)
	}
}

// Keep this in sync with the respective upstream set
// WatchEvent does not have TypeMeta and cannot be roundtripped.
var nonInternalRoundTrippableTypes = sets.NewString("List", "ListOptions", "WatchEvent")

// TestTypes will try to roundtrip all OpenShift and Kubernetes stable api types
func TestTypes(t *testing.T) {
	internalVersionToExternalVersions := map[unversioned.GroupVersion][]unversioned.GroupVersion{
		osapi.SchemeGroupVersion:    {v1.SchemeGroupVersion},
		quotaapi.SchemeGroupVersion: {quotaapiv1.SchemeGroupVersion},
	}

	for internalVersion, externalVersions := range internalVersionToExternalVersions {
		for kind, reflectType := range kapi.Scheme.KnownTypes(internalVersion) {
			if !strings.Contains(reflectType.PkgPath(), "github.com/openshift/origin/") && reflectType.PkgPath() != "github.com/openshift/origin/vendor/k8s.io/kubernetes/pkg/api" {
				continue
			}
			if nonInternalRoundTrippableTypes.Has(kind) {
				continue
			}

			for _, externalVersion := range externalVersions {
				// Try a few times, since runTest uses random values.
				for i := 0; i < fuzzIters; i++ {
					item, err := kapi.Scheme.New(internalVersion.WithKind(kind))
					if err != nil {
						t.Errorf("Couldn't make a %v? %v", kind, err)
						continue
					}
					if _, err := meta.TypeAccessor(item); err != nil {
						t.Fatalf("%q is not a TypeMeta and cannot be tested - add it to nonRoundTrippableTypes: %v", kind, err)
					}
					seed := rand.Int63()

					if versions, ok := skipStandardVersions[kind]; ok {
						for _, v := range versions {
							fuzzInternalObject(t, v, item, seed)
							roundTrip(t, kapi.Codecs.LegacyCodec(v), item)
						}
						continue
					}
					fuzzInternalObject(t, externalVersion, item, seed)
					roundTripWithAllCodecs(t, externalVersion, item)
				}
			}
		}

	}
}
