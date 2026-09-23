# ratify-crd-converter

离线 CLI：把 Ratify **v1** CRD（`config.ratify.deislabs.io/v1beta1`）迁移聚合成 **v2**
`Executor` / `NamespacedExecutor`（`config.ratify.sh/v2beta1`）。

## 为什么是"聚合器"而非 1:1 转换
v1 是一堆独立 CR（Store / Verifier / Policy / KeyManagementProvider / CertificateStore），
v2 把它们全塞进一个 `Executor`。所以工具吃一批 v1 CR，按 scope（cluster / 每个 namespace）
聚合成 Executor。

## 用法
```bash
# 目录（递归所有 .yaml）
ratify-convert -f ./v1-manifests/ -o executor.yaml

# 多个文件 + 兜底 scope
ratify-convert -f store.yaml -f verifier.yaml -f kmp.yaml --scope "myregistry.io/*"
```

| flag | 说明 |
|---|---|
| `-f, --file` | 输入文件或目录，可重复 |
| `-o, --output` | 输出文件，默认 stdout |
| `--scope` | 无法从 verifier 推导时的兜底 scope |
| `--concurrency` | Executor.spec.concurrency，0 = v2 默认 |
| `--name` | 生成 executor 的 metadata.name |

## 映射规则（摘要）
- **Store**：`spec.name` → `type`（`oras`→`registry-store`）；`parameters` 直传；`address`/`source` 非空则告警丢弃。
- **Verifier**：`metadata.name` → 实例 `name`，`spec.name` → `type`；`artifactTypes` 折进 `parameters`。
- **Policy**：`type`/`parameters` 直传；多个 → 取一并告警。
- **KMP / CertificateStore**：被 verifier 的 `verificationCertStores` 引用时，内联进该 verifier
  的 `parameters.certificates[]`（`inline`→`inline.certs`，`azurekeyvault`→`azurekeyvault.vaultURL`）；
  未被引用 → 孤儿告警。
- **scopes**：默认从 verifier 提取（notation `trustPolicyDoc.trustPolicies[].registryScopes`、
  cosign `trustPolicies[].scopes`）；含 `*` 或多 trust policy 不一致 → `["*"]`。

## 无法自动映射（会告警）
1. Store/Verifier 的 `address` / `source`（v2 无外部插件下载机制）
2. KMP 的 `tenantID` / `clientID`（v2 用 workload identity）
3. 孤儿 KMP、无 stores/verifiers 的分组、多 Policy

## 结构
```
cmd/ratify-convert/   CLI (cobra)
internal/apis/v1beta1  源类型
internal/apis/v2beta1  目标类型
internal/loader        多文档 YAML -> Bundle
internal/convert       转换器 + 引用解析 + scope 提取 + 聚合器
internal/report        告警收集
testdata/              golden 用例
```

## 开发
```bash
go build ./...
go test ./...
go run ./cmd/ratify-convert -f testdata/v1_bundle.yaml
```
