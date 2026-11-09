-- +goose Up
CREATE TABLE rate_limit_rules (
 key text PRIMARY KEY,
 limit_count int NOT NULL CHECK (limit_count >= 1),
 window_seconds int NOT NULL CHECK (window_seconds >= 0),
 ban_seconds int NOT NULL DEFAULT 0 CHECK (ban_seconds >= -1),
 enabled boolean NOT NULL DEFAULT true,
 updated_by uuid REFERENCES admin_users(id) ON DELETE SET NULL,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX identities_created_ip_created_at_idx ON identities (created_ip, created_at);
INSERT INTO rate_limit_rules (key,limit_count,window_seconds,ban_seconds) VALUES
 ('identity_create',5,86400,86400),
 ('msg_per_second',1,1,0),
 ('msg_per_minute',10,60,0),
 ('room_msg_interval',1,1,0),
 ('match_start',15,60,0),
 ('ws_conn_per_ip_identity',1,0,0),
 ('ws_conn_per_ip',3,0,0),
 ('sms_per_phone',1,60,0),
 ('sms_per_phone_daily',5,86400,0),
 ('sms_per_ip_daily',20,86400,0),
 ('bottle_post',10,86400,0),
 ('bottle_pick',50,86400,0),
 ('private_link_create',20,86400,0),
 ('private_link_password',5,600,0),
 ('admin_login',5,900,0),
 ('report_daily',20,86400,0),
 ('nickname_shuffle',10,60,0),
 ('nickname_set',10,86400,0),
 ('vip_redeem_per_id',5,600,0),
 ('vip_redeem_per_ip',20,600,0),
 ('broadcast_post',5,86400,0),
 ('story_create',10,86400,0),
 ('bot_settings_test',10,60,0);

-- +goose Down
DROP TABLE rate_limit_rules;
DROP INDEX identities_created_ip_created_at_idx;
