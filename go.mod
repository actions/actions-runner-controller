module github.com/actions/actions-runner-controller

go 1.24.3

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.17.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.8.1
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets v1.3.0
	github.com/bradleyfalzon/ghinstallation/v2 v2.14.0
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc
	github.com/evanphx/json-patch v5.9.11+incompatible
	github.com/go-logr/logr v1.4.2
	github.com/golang-jwt/jwt/v4 v4.5.2
	github.com/google/go-cmp v0.7.0
	github.com/google/go-github/v52 v52.0.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gregjones/httpcache v0.0.0-20190611155906-901d90724c79
	github.com/gruntwork-io/terratest v0.48.2
	github.com/hashicorp/go-retryablehttp v0.7.7
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/ginkgo/v2 v2.23.3
	github.com/onsi/gomega v1.36.3
	github.com/prometheus/client_golang v1.21.1
	github.com/stretchr/testify v1.10.0
	github.com/teambition/rrule-go v1.8.2
	go.uber.org/multierr v1.11.0
	go.uber.org/zap v1.27.0
	golang.org/x/net v0.38.0
	golang.org/x/oauth2 v0.28.0
	golang.org/x/sync v0.12.0
	gomodules.xyz/jsonpatch/v2 v2.5.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.32.3
	k8s.io/apimachinery v0.32.3
	k8s.io/client-go v0.32.3
	k8s.io/utils v0.0.0-20241210054802-24370beab758
	sigs.k8s.io/controller-runtime v0.20.4
	sigs.k8s.io/yaml v1.4.0
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.10.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/internal v1.1.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.3.2 // indirect
	github.com/BurntSushi/toml v1.4.0 // indirect
	github.com/ProtonMail/go-crypto v1.1.6 // indirect
	github.com/aws/aws-sdk-go-v2 v1.36.3 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.10 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.29.9 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.62 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.30 // indirect
	github.com/aws/aws-sdk-go-v2/feature/s3/manager v1.17.65 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/acm v1.31.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.52.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.46.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.41.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.208.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecr v1.42.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ecs v1.54.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/iam v1.40.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.6.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.10.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.18.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/kms v1.38.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/lambda v1.70.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/rds v1.94.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/route53 v1.49.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.78.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.35.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.34.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.38.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssm v1.57.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.25.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.29.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.17 // indirect
	github.com/aws/smithy-go v1.22.3 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/boombuler/barcode v1.0.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.6.1 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.6 // indirect
	github.com/emicklei/go-restful/v3 v3.12.2 // indirect
	github.com/evanphx/json-patch/v5 v5.9.11 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-errors/errors v1.5.1 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-sql-driver/mysql v1.9.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/gonvenience/bunt v1.4.0 // indirect
	github.com/gonvenience/idem v0.0.1 // indirect
	github.com/gonvenience/neat v1.3.15 // indirect
	github.com/gonvenience/term v1.0.3 // indirect
	github.com/gonvenience/text v1.0.8 // indirect
	github.com/gonvenience/ytbx v1.4.6 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/gnostic-models v0.6.9 // indirect
	github.com/google/go-github/v69 v69.2.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20250302191652-9094ed2288e7 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/gruntwork-io/go-commons v0.17.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/homeport/dyff v1.10.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/mattn/go-ciede2000 v0.0.0-20170301095244-782e8c62fec3 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-zglob v0.0.6 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/go-ps v1.0.0 // indirect
	github.com/mitchellh/hashstructure v1.1.0 // indirect
	github.com/moby/spdystream v0.5.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/pquerna/otp v1.4.0 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/texttheater/golang-levenshtein v1.0.1 // indirect
	github.com/urfave/cli/v2 v2.27.6 // indirect
	github.com/virtuald/go-ordered-json v0.0.0-20170621173500-b18e6e673d74 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/exp v0.0.0-20250305212735-054e65f0b394 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.31.0 // indirect
	google.golang.org/protobuf v1.36.5 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.32.2 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20250304201544-e5f78fe3ede9 // indirect
	sigs.k8s.io/json v0.0.0-20241014173422-cfa47c3a1cc8 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.6.0 // indirect
)

replace github.com/gregjones/httpcache => github.com/actions-runner-controller/httpcache v0.2.0
