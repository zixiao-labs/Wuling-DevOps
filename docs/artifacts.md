# Artifact Service

Stage 2 将制品拆成两个边界：

- `wuling-api` 管理 Package、Version、Release 元数据与项目权限；
- `wuling-artifacts` 只管理不可变 Blob，可独立扩容和挂载对象存储。

Stage 2.3 增加浏览器手动上传闭环。用户在项目的 Artifacts 页面选择已注册的
Package、版本号和文件；浏览器只访问 `wuling-api`，由主 API 完成项目权限和版本
唯一性校验后，使用内部令牌把文件转发到 `wuling-artifacts`。成功响应会记录文件
大小、SHA-256、Content-Type 和原始文件名。

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

主 API 通过以下配置访问该内部接口：

- `WULING_ARTIFACTS_BASE_URL`（Docker Compose 中为 `http://artifacts:8090`）
- `WULING_ARTIFACTS_INTERNAL_TOKEN`
- `WULING_ARTIFACTS_MAX_UPLOAD_BYTES`

面向用户的上传接口为
`POST /api/v1/orgs/{org}/projects/{project}/packages/{package_id}/uploads`，
版本通过必填的 `version` query 参数传入，请求体使用 `multipart/form-data` 且只包含
一个 `file` 文件字段。主 API 将文件流式转发，不会先把整个大文件缓存在内存或本地
磁盘。上传需要项目 `developer` 或更高角色。

## 对象存储

`aws`、`r2` 使用 S3-compatible API，`oss` 使用阿里云 OSS API；它们都需要设置：

- `WULING_ARTIFACTS_STORAGE_ENDPOINT`
- `WULING_ARTIFACTS_STORAGE_REGION`
- `WULING_ARTIFACTS_STORAGE_BUCKET`
- `WULING_ARTIFACTS_STORAGE_ACCESS_KEY`
- `WULING_ARTIFACTS_STORAGE_SECRET_KEY`
- `WULING_ARTIFACTS_STORAGE_TLS`

Cloudflare R2 的 region 通常使用 `auto`；AWS 使用 bucket 所在 region；阿里云
OSS 使用对应地域的 endpoint 和 region。Bucket 必须预先创建，服务的
`/healthz` 会验证它可访问。

不可变写入要求对象存储提供原子 create-if-absent 语义。OSS 使用原生
`x-oss-forbid-overwrite`；其他 S3-compatible provider 必须正确实现
`If-None-Match: *`。服务不会使用存在竞态的“先 HEAD 再 PUT”回退，不支持条件写的
provider 不应承载制品 Blob。

Blob key 由主 API 生成，格式为
`projects/{project_id}/packages/{package_id}/{version}`。服务会拒绝空段、绝对路径和
`..`，也会拒绝本地元数据保留段 `.metadata`；本地写入先落临时文件再原子替换，
避免中断上传留下半个制品。
