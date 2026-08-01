-- New talkgroups opt in to transcription while existing explicit choices remain unchanged.
ALTER TABLE talkgroup_aliases ALTER COLUMN transcription_enabled SET DEFAULT true;
