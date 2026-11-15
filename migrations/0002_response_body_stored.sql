-- 0002: record whether the answer body was stored for a response.
--
-- The owner's C1 contract (seo-operations O20, 2026-11-11) says the answer text
-- must not be kept in a Git-tracked database. `aicm run --store hash-only` (or
-- `store_answer_body = false`) writes the answer hash and nothing else, so a
-- verifier needs to know that an empty text is a choice, not a lost answer.
--
-- 1 (the default) keeps the behaviour of every row written before this column
-- existed: the body is there and the hash can be checked against it.
ALTER TABLE responses ADD COLUMN body_stored INTEGER NOT NULL DEFAULT 1
    CHECK (body_stored IN (0, 1));
