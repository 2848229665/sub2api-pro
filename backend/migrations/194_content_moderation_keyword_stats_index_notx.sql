-- 加速关键词命中统计页的全量与时间范围聚合查询。
-- 仅索引 matched_keyword 非空的命中记录，避免放大普通审核日志的索引体积。

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_content_moderation_logs_keyword_hits_created_at
    ON content_moderation_logs (created_at DESC)
    INCLUDE (user_id, user_email, matched_keyword)
    WHERE matched_keyword <> '';
