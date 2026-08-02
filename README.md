# RateWatch 倍率同步平台

[GitHub 开源仓库](https://github.com/JnmHub/ratewatch) · [MIT License](./LICENSE)

RateWatch 使用普通上游 API Key 监听实际生效倍率，再按每条任务的固定增减值，单向同步到你拥有管理员权限的 New API 或 Sub2API 站点。它的目标不是做 API 代理，而是防止上游临时调价让下游倒挂亏损。

## 已实现能力

- 多用户注册和严格租户隔离；所有站点、Key、任务、事件和邮件设置均按用户归属。
- 管理员 Key/普通 Key 使用 AES-256-GCM 加密落库，接口和页面仅返回脱敏值。
- 一键导入自有站点的分组、渠道/账号、模型、现有倍率和关联树。
- Sub2API 普通 Key 通过 `/v1/sub2api/billing` 直接读取生效倍率。
- New API 普通 Key发送最小文本请求，通过 `X-Oneapi-Request-Id` 匹配 `/api/log/token` 中的 `other.group_ratio`。
- 一个同步任务可聚合多个上游；每个目标分组独立使用 `max(最高有效上游倍率, 最低上游倍率) + 固定增减值`。
- 同一目标分组仅允许一个启用中的任务；结果 `<= 0`、余额不足、探测失败时不写入。
- New API 写入前读取完整 `GroupRatio`，合并目标分组后整体写回，不覆盖其它分组。
- Sub2API 仅部分更新目标分组的 `rate_multiplier`。
- 模型列表每 30 分钟比较，只告警差异，不自动增删。
- 生图不主动请求；New API 文本探测读取计费日志时，会用同一份日志被动提取真实生图请求的模型、尺寸、质量、张数、分组倍率、单价和扣费，并按 request ID 去重；`POST /api/image-observations` 也可接收外部真实流量观测。无法精确映射目标计费维度时只记录和通知。
- SSE 网页实时通知；SMTP 邮件默认每 10 分钟合并摘要，租户可选择事件类型。
- 每个上游保留最近 30 次健康状态，绿色表示正常无变化、蓝色表示变化且已同步，黄/橙/红色分别表示待处理、写入失败或检查失败。
- 日志保存站点、上游、目标分组、失败原因和修改前后倍率；常规成功轮询不落事件表。
- 独立系统管理后台支持动态访问路径、聚合运维统计、注册开关、调度周期和 SMTP 设置。
- 支持个人邮箱/密码修改、邮件找回密码，以及可跳步的首次使用引导。
- FreeModel 风格 Vue 3 后台，支持跟随系统、亮色、暗色、桌面和移动端。
- 生产保护包含单分组单写入者、结果下限、可选观察模式、大变化告警、写入前确认分组仍存在、写入后回读校验和安全响应头。

详细实施与验收状态见 [计划和清单.md](./计划和清单.md)。

## 默认管理员账号

| 项目 | 默认值 |
| --- | --- |
| 管理员账号 | `tokenav` |
| 初始密码 | `123456` |
| 管理后台 | `/admin` |

系统会在首次启动时自动创建该管理员。首次登录后请立即前往“个人信息”修改密码；管理后台路径也可以在后台系统设置中修改。

## 快速启动

### Docker Compose

1. 复制 `.env.example` 为 `.env`。
2. 分别生成两组随机密钥：

```powershell
[Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
```

3. 将两次结果填入 `RATEWATCH_MASTER_KEY` 和 `RATEWATCH_SESSION_SECRET`。
4. 启动：

```bash
docker compose up -d --build
```

浏览器访问 `http://localhost:8080`。

### 本地开发

后端：

```powershell
$env:RATEWATCH_MASTER_KEY='<base64>'
$env:RATEWATCH_SESSION_SECRET='<base64>'
go run ./cmd/server
```

前端：

```powershell
cd web
npm install
npm run dev
```

Vite 会把 `/api` 代理到 `localhost:8080`。正式发布的二进制已经内置管理网页，下载后可以直接运行；如需用自定义前端覆盖内置版本，可通过 `RATEWATCH_WEB_DIR` 指定前端目录。

## 站点接入说明

### New API 目标站点

- 默认发送 `Authorization: Bearer <管理员令牌>` 和 `New-Api-User: 1`。
- 如果管理员用户 ID 不是 `1`，添加站点时修改该字段。
- 已有渠道列表通常不会返回明文 Key，RateWatch 不会绕过二次验证；要监听该上游时，请手动添加一次普通 Key。

### Sub2API 目标站点

- 当前官方源码的永久管理员 Key 使用 `x-api-key: <永久管理员 Key>`；添加站点时将管理员 Header 设为 `x-api-key`。
- 若其它分支使用 `Authorization`、`X-Admin-Key` 等 Header，可在添加站点时修改“管理员 Header”；只有 Authorization 会自动加 `Bearer`。
- 只处理 API-Key 型上游账号，不导入 OAuth、Setup Token 或会话凭证。
- 自动创建 API-Key 账号时使用 `type: apikey`、`platform: openai`、数字型 `group_ids`，Base URL 写入 `extra.base_url`。

不同 fork 的管理员端点/字段可能有差异。连接器集中在 `internal/connectors/managed.go`，兼容调整不影响同步引擎。

## 调度与失败策略

| 项目 | 默认周期/行为 |
| --- | --- |
| Sub2API 计费轮询 | 45 秒 |
| New API 文本探测 | 5 分钟 |
| New API 日志匹配 | 探测后每 2 秒，最多约 12 秒 |
| 模型列表检查 | 30 分钟，仅通知 |
| 邮件摘要 | 10 分钟 |
| 有效倍率变化 | 当轮立即写入 |
| 探测失败/余额不足 | 保留最后成功值，不写入 |
| 目标分组删除 | 标记删除、停止写入、告警，不重建 |

## 验证

```bash
go test ./...
cd web
npm run typecheck
npm run build
```

测试包含 New API request ID 日志匹配、Sub2API 有效倍率优先级、凭证加密/会话，以及完整的倍率聚合和目标写入闭环。

### 官方源码本地实测

使用项目方提供的 New API 与 Sub2API 源码压缩包各部署两套独立实例，已验证：

| 上游 → 目标 | 上游倍率 | 固定增减 | 目标实际倍率 | 结果 |
| --- | ---: | ---: | ---: | --- |
| New API → New API | 1.1 | +0.2 | 1.3 | 通过 |
| Sub2API → Sub2API | 1.2 | +0.1 | 1.3 | 通过 |
| New API → Sub2API | 1.1 | +0.15 | 1.25 | 通过 |
| Sub2API → New API | 1.2 | -0.1 | 1.1 | 通过 |

同时验证了两种目标站的分组/渠道树导入、跨平台自动创建 API-Key 账号或渠道、Sub2API 零余额普通 Key 直读倍率，以及 New API 无人干预轮询后的自动写入。New API 的 `/api/log/token` 有 Critical Rate Limit，代码会复用同一次日志响应完成文本倍率匹配和生图观测，避免重复请求自我限流。

生图价格目前属于“可观测、不可保证自动写入”：Sub2API 普通 Key 的 `/v1/sub2api/billing` 明确只返回 token 倍率；New API `/api/pricing` 可能同时暴露多个可用分组，不能据此确定普通 Key 的实际分组。只有真实请求日志能提供可靠事实，而且 New API 与 Sub2API 的目标计费字段也不完全同构，因此无法精确映射时只告警，不冒险修改全局模型价格。

## 安全提示

- `RATEWATCH_MASTER_KEY` 丢失后无法恢复已保存凭证；泄露后应重新录入所有密钥并轮换主密钥。
- 生产环境必须使用 HTTPS，并限制后台的公开访问面。
- 不要在日志、截图、Issue 或导出的数据库中粘贴明文 Key。
- 本项目只允许上游到目标的单向同步，不提供反向写入或跨租户共享。
