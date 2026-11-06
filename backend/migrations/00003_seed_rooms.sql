-- +goose Up
-- 6 个官方房间：每个先建一个 kind=room 的会话，再插入 rooms。
-- +goose StatementBegin
DO $$
DECLARE
    r RECORD;
    conv_id uuid;
BEGIN
    FOR r IN
        SELECT * FROM (VALUES
            ('lobby',      '闲聊大厅', '随便聊聊，认识新朋友', 1),
            ('late-night', '深夜树洞', '夜深了，把心事说出来', 2),
            ('gaming',     '游戏',     '开黑、组队、聊游戏',     3),
            ('study',      '学习打卡', '一起学习，互相监督',     4),
            ('emotion',    '情感',     '关于喜欢、关系和烦恼',   5),
            ('work',       '职场',     '工作、面试与吐槽',       6)
        ) AS t(slug, name, descr, ord)
    LOOP
        INSERT INTO conversations (kind) VALUES ('room') RETURNING id INTO conv_id;
        INSERT INTO rooms (conversation_id, slug, name, description, sort_order)
        VALUES (conv_id, r.slug, r.name, r.descr, r.ord);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    conv_ids uuid[];
BEGIN
    SELECT array_agg(conversation_id) INTO conv_ids
    FROM rooms
    WHERE slug IN ('lobby', 'late-night', 'gaming', 'study', 'emotion', 'work');

    DELETE FROM rooms
    WHERE slug IN ('lobby', 'late-night', 'gaming', 'study', 'emotion', 'work');

    IF conv_ids IS NOT NULL THEN
        DELETE FROM conversations WHERE id = ANY(conv_ids);
    END IF;
END $$;
-- +goose StatementEnd
