//go:build !ignore_autogenerated
// +build !ignore_autogenerated

/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/api/core/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingListener) DeepCopyInto(out *AutoscalingListener) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingListener.
func (in *AutoscalingListener) DeepCopy() *AutoscalingListener {
	if in == nil {
		return nil
	}
	out := new(AutoscalingListener)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AutoscalingListener) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingListenerList) DeepCopyInto(out *AutoscalingListenerList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]AutoscalingListener, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingListenerList.
func (in *AutoscalingListenerList) DeepCopy() *AutoscalingListenerList {
	if in == nil {
		return nil
	}
	out := new(AutoscalingListenerList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AutoscalingListenerList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingListenerSpec) DeepCopyInto(out *AutoscalingListenerSpec) {
	*out = *in
	if in.ImagePullSecrets != nil {
		in, out := &in.ImagePullSecrets, &out.ImagePullSecrets
		*out = make([]v1.LocalObjectReference, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingListenerSpec.
func (in *AutoscalingListenerSpec) DeepCopy() *AutoscalingListenerSpec {
	if in == nil {
		return nil
	}
	out := new(AutoscalingListenerSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingListenerStatus) DeepCopyInto(out *AutoscalingListenerStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingListenerStatus.
func (in *AutoscalingListenerStatus) DeepCopy() *AutoscalingListenerStatus {
	if in == nil {
		return nil
	}
	out := new(AutoscalingListenerStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingRunnerSet) DeepCopyInto(out *AutoscalingRunnerSet) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingRunnerSet.
func (in *AutoscalingRunnerSet) DeepCopy() *AutoscalingRunnerSet {
	if in == nil {
		return nil
	}
	out := new(AutoscalingRunnerSet)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AutoscalingRunnerSet) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingRunnerSetList) DeepCopyInto(out *AutoscalingRunnerSetList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]AutoscalingRunnerSet, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingRunnerSetList.
func (in *AutoscalingRunnerSetList) DeepCopy() *AutoscalingRunnerSetList {
	if in == nil {
		return nil
	}
	out := new(AutoscalingRunnerSetList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *AutoscalingRunnerSetList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingRunnerSetSpec) DeepCopyInto(out *AutoscalingRunnerSetSpec) {
	*out = *in
	if in.Proxy != nil {
		in, out := &in.Proxy, &out.Proxy
		*out = new(ProxyConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.GitHubServerTLS != nil {
		in, out := &in.GitHubServerTLS, &out.GitHubServerTLS
		*out = new(GitHubServerTLSConfig)
		**out = **in
	}
	in.Template.DeepCopyInto(&out.Template)
	if in.MaxRunners != nil {
		in, out := &in.MaxRunners, &out.MaxRunners
		*out = new(int)
		**out = **in
	}
	if in.MinRunners != nil {
		in, out := &in.MinRunners, &out.MinRunners
		*out = new(int)
		**out = **in
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingRunnerSetSpec.
func (in *AutoscalingRunnerSetSpec) DeepCopy() *AutoscalingRunnerSetSpec {
	if in == nil {
		return nil
	}
	out := new(AutoscalingRunnerSetSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *AutoscalingRunnerSetStatus) DeepCopyInto(out *AutoscalingRunnerSetStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new AutoscalingRunnerSetStatus.
func (in *AutoscalingRunnerSetStatus) DeepCopy() *AutoscalingRunnerSetStatus {
	if in == nil {
		return nil
	}
	out := new(AutoscalingRunnerSetStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunner) DeepCopyInto(out *EphemeralRunner) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunner.
func (in *EphemeralRunner) DeepCopy() *EphemeralRunner {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunner)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EphemeralRunner) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerList) DeepCopyInto(out *EphemeralRunnerList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]EphemeralRunner, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerList.
func (in *EphemeralRunnerList) DeepCopy() *EphemeralRunnerList {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EphemeralRunnerList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerSet) DeepCopyInto(out *EphemeralRunnerSet) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerSet.
func (in *EphemeralRunnerSet) DeepCopy() *EphemeralRunnerSet {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerSet)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EphemeralRunnerSet) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerSetList) DeepCopyInto(out *EphemeralRunnerSetList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]EphemeralRunnerSet, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerSetList.
func (in *EphemeralRunnerSetList) DeepCopy() *EphemeralRunnerSetList {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerSetList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *EphemeralRunnerSetList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerSetSpec) DeepCopyInto(out *EphemeralRunnerSetSpec) {
	*out = *in
	in.EphemeralRunnerSpec.DeepCopyInto(&out.EphemeralRunnerSpec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerSetSpec.
func (in *EphemeralRunnerSetSpec) DeepCopy() *EphemeralRunnerSetSpec {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerSetSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerSetStatus) DeepCopyInto(out *EphemeralRunnerSetStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerSetStatus.
func (in *EphemeralRunnerSetStatus) DeepCopy() *EphemeralRunnerSetStatus {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerSetStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerSpec) DeepCopyInto(out *EphemeralRunnerSpec) {
	*out = *in
	if in.Proxy != nil {
		in, out := &in.Proxy, &out.Proxy
		*out = new(ProxyConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.GitHubServerTLS != nil {
		in, out := &in.GitHubServerTLS, &out.GitHubServerTLS
		*out = new(GitHubServerTLSConfig)
		**out = **in
	}
	in.PodTemplateSpec.DeepCopyInto(&out.PodTemplateSpec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerSpec.
func (in *EphemeralRunnerSpec) DeepCopy() *EphemeralRunnerSpec {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *EphemeralRunnerStatus) DeepCopyInto(out *EphemeralRunnerStatus) {
	*out = *in
	if in.Failures != nil {
		in, out := &in.Failures, &out.Failures
		*out = make(map[string]bool, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new EphemeralRunnerStatus.
func (in *EphemeralRunnerStatus) DeepCopy() *EphemeralRunnerStatus {
	if in == nil {
		return nil
	}
	out := new(EphemeralRunnerStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *GitHubServerTLSConfig) DeepCopyInto(out *GitHubServerTLSConfig) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new GitHubServerTLSConfig.
func (in *GitHubServerTLSConfig) DeepCopy() *GitHubServerTLSConfig {
	if in == nil {
		return nil
	}
	out := new(GitHubServerTLSConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ProxyConfig) DeepCopyInto(out *ProxyConfig) {
	*out = *in
	if in.HTTP != nil {
		in, out := &in.HTTP, &out.HTTP
		*out = new(ProxyServerConfig)
		(*in).DeepCopyInto(*out)
	}
	if in.HTTPS != nil {
		in, out := &in.HTTPS, &out.HTTPS
		*out = new(ProxyServerConfig)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ProxyConfig.
func (in *ProxyConfig) DeepCopy() *ProxyConfig {
	if in == nil {
		return nil
	}
	out := new(ProxyConfig)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ProxyServerConfig) DeepCopyInto(out *ProxyServerConfig) {
	*out = *in
	if in.NoProxy != nil {
		in, out := &in.NoProxy, &out.NoProxy
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ProxyServerConfig.
func (in *ProxyServerConfig) DeepCopy() *ProxyServerConfig {
	if in == nil {
		return nil
	}
	out := new(ProxyServerConfig)
	in.DeepCopyInto(out)
	return out
}
