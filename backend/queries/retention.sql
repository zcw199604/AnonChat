-- ---- 保留期清理（批量 5000，截止时间由调用方注入）----

-- name: DeleteExpiredMessages :execrows
WITH batch AS (
    SELECT m.id FROM messages m WHERE m.created_at < $1 LIMIT 5000
)
DELETE FROM messages WHERE id IN (SELECT id FROM batch);

-- name: DeleteBottlePicksForEmptyEndedConversations :execrows
DELETE FROM bottle_picks
WHERE conversation_id IN (
    SELECT c.id FROM conversations c
    WHERE c.ended_at IS NOT NULL AND c.ended_at < $1 AND c.kind <> 'room'
      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = c.id)
);

-- name: DeletePrivateLinksForEmptyEndedConversations :execrows
DELETE FROM private_links
WHERE conversation_id IN (
    SELECT c.id FROM conversations c
    WHERE c.ended_at IS NOT NULL AND c.ended_at < $1 AND c.kind <> 'room'
      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = c.id)
);

-- name: DeleteEmptyEndedConversations :execrows
WITH batch AS (
    SELECT c.id FROM conversations c
    WHERE c.ended_at IS NOT NULL AND c.ended_at < $1 AND c.kind <> 'room'
      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = c.id)
    LIMIT 5000
)
DELETE FROM conversations WHERE id IN (SELECT id FROM batch);

-- name: DeleteExpiredBottles :execrows
WITH batch AS (
    SELECT b.id FROM bottles b WHERE b.expires_at < $1 LIMIT 5000
)
DELETE FROM bottles WHERE id IN (SELECT id FROM batch);

-- name: DeleteOldBottlePicks :execrows
DELETE FROM bottle_picks WHERE picked_at < $1;

-- name: DeleteExpiredPrivateLinks :execrows
DELETE FROM private_links WHERE expires_at < $1 OR (destroyed_at IS NOT NULL AND destroyed_at < $1);

-- name: DeleteOldSmsCodes :execrows
DELETE FROM sms_codes WHERE created_at < $1;

-- name: DeleteOldAccessLogs :execrows
DELETE FROM access_logs WHERE created_at < $1;

-- name: DeleteUnreferencedRevokedIdentities :execrows
WITH batch AS (
    SELECT i.id FROM identities i
    WHERE i.revoked_at IS NOT NULL AND i.revoked_at < $1
      AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.sender_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM conversation_members cm WHERE cm.identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM bottles b WHERE b.identity_id = i.id OR b.held_by = i.id)
      AND NOT EXISTS (SELECT 1 FROM bottle_picks bp WHERE bp.picker_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM private_links pl WHERE pl.creator_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM blocks bl WHERE bl.blocker_identity_id = i.id OR bl.blocked_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM reports r WHERE r.reporter_identity_id = i.id OR r.reported_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM rooms rm WHERE rm.owner_identity_id = i.id)
      AND NOT EXISTS (SELECT 1 FROM bans ba WHERE ba.subject_type = 'identity' AND ba.subject_value = i.id::text)
    LIMIT 5000
)
DELETE FROM identities WHERE id IN (SELECT id FROM batch);

-- name: DeleteOldReports :execrows
WITH batch AS (
    SELECT r.id FROM reports r WHERE r.created_at < $1 LIMIT 5000
)
DELETE FROM reports WHERE id IN (SELECT id FROM batch);
