output "cluster_endpoint" {
  description = "Endpoint for EKS control plane"
  value       = module.eks.cluster_endpoint
}

output "cluster_security_group_id" {
  description = "Security group ids"
  value       = module.eks.cluster_security_group_id
}

output "cluster_name" {
  description = "Cluster Name"
  value       = module.eks.cluster_name
}