package controllers

import (
	corev1 "k8s.io/api/core/v1"
)

func filterEnvVars(envVars []corev1.EnvVar, filter string) (filtered []corev1.EnvVar) {
	for _, envVar := range envVars {
		if envVar.Name != filter {
			filtered = append(filtered, envVar)
		}
	}
	return filtered
}
