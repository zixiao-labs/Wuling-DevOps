# Artifact Service

Stage 2 将制品拆成两个边界：

- `wuling-api` 管理 Package、Version、Release 元数据与项目权限；
- `wuling-artifacts` 只管理不可变 Blob，可独立扩容和挂载对象存储。

## 本地启动

```bash
WULING_ARTIFACTS_INTERNAL_TOKEN=dev-token \
WULING_ARTIFACTS_STORAGE_PROVIDER=local \
WULING_ARTIFACTS_LOCAL_DIR=./var/artifacts \
go run ./cmd/wuling-artifacts
```

健康检查为 `GET /healthz`。内部 Blob API 位于 `/v1/blobs/{key}`，支持
`PUT`、`GET`、`HEAD`、`DELETE`，请求需携带
`Authorization: Bearer <WULING_ARTIFACTS_INTERNAL_TOKEN>`。

## 对象存储

`aws`、`r2`、`oss` 统一走 S3-compatible endpoint，需要设置：

- `WULING_ARTIFACTS_STORAGE_ENDPOINT`
- `WULING_ARTIFACTS_STORAGE_REGION`
- `WULING_ARTIFACTS_STORAGE_BUCKET`
- `WULING_ARTIFACTS_STORAGE_ACCESS_KEY`
- `WULING_ARTIFACTS_STORAGE_SECRET_KEY`
- `WULING_ARTIFACTS_STORAGE_TLS`

Cloudflare R2 的 region 通常使用 `auto`；AWS 使用 bucket 所在 region；阿里云
OSS 使用对应地域的 S3 兼容 endpoint。Bucket 必须预先创建，服务的
`/healthz` 会验证它可访问。

Blob key 由主 API 生成，格式为
`projects/{project_id}/packages/{package_id}/{version}`。服务会拒绝空段、绝对路径和
`..`，本地写入先落临时文件再原子替换，避免中断上传留下半个制品。
