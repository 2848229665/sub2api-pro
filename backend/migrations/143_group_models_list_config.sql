-- 分组级自定义 /v1/models 展示列表配置。
-- 可选将展示列表同时作为请求模型白名单；不参与账号选择、模型映射或网关调度。

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb;
