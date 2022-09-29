# cert-manager must be deployed or included via the deployment process

resource "kubernetes_namespace" "cm" {
  metadata {
    name = "cert-manager"
  }
}

resource "helm_release" "cm" {
  count            = var.actions_runner_controller
  name             = "cm"
  namespace        = kubernetes_namespace.cm.metadata[0].name
  create_namespace = true
  chart            = "cert-manager"
  repository       = "https://charts.jetstack.io"
  version          = "v1.8.0"
  values = [<<EOF
    global:
      podSecurityPolicy:
        enabled: true
        useAppArmor: true
    prometheus:
      enabled: false
    installCRDs: true
  EOF
  ]
}
