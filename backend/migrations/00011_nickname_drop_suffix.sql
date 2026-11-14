-- +goose Up
-- 昵称不再带“#编号”：把历史上由生成器产出的“形容词的名词#1234”去掉编号后缀。
-- 只处理生成器格式（含“的”且以 #4 位数字结尾），自定义昵称（如“小明#1234”“abc”）保持原样。
UPDATE identities
SET nickname = regexp_replace(nickname, '#[0-9]{4}$', '')
WHERE nickname ~ '^[^#]+的[^#]+#[0-9]{4}$';

-- 外站绑定里保存的昵称同样去掉编号后缀（外站会拒绝含过多数字的昵称）。
UPDATE external_bindings
SET nickname = regexp_replace(nickname, '#[0-9]{4}$', '')
WHERE nickname ~ '^[^#]+的[^#]+#[0-9]{4}$';

-- +goose Down
-- 去编号是有损操作：原编号为随机数且没有留档，无法自动还原，因此回滚为空操作。
SELECT 1;
