ALTER TABLE calls ADD COLUMN IF NOT EXISTS notes_updated_at timestamptz;
ALTER TABLE calls ADD COLUMN IF NOT EXISTS notes_updated_by text;
CREATE INDEX IF NOT EXISTS calls_site_idx ON calls(site_id);
CREATE INDEX IF NOT EXISTS calls_receiver_idx ON calls(receiver_id);
CREATE INDEX IF NOT EXISTS calls_type_idx ON calls(call_type);
CREATE INDEX IF NOT EXISTS calls_frequency_idx ON calls(frequency);
CREATE INDEX IF NOT EXISTS calls_duration_idx ON calls(duration_ms);
ALTER TABLE calls ADD COLUMN IF NOT EXISTS search_document tsvector;
CREATE OR REPLACE FUNCTION calls_search_document_refresh() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.search_document := to_tsvector('simple'::regconfig,
    coalesce(NEW.system_id,'') || ' ' || coalesce(NEW.system_name,'') || ' ' ||
    coalesce(NEW.site_id,'') || ' ' || coalesce(NEW.site_name,'') || ' ' ||
    coalesce(NEW.receiver_id,'') || ' ' || coalesce(NEW.talkgroup_id,'') || ' ' ||
    coalesce(NEW.talkgroup_name,'') || ' ' || coalesce(NEW.radio_id,'') || ' ' ||
    coalesce(NEW.radio_name,'') || ' ' || coalesce(NEW.frequency,'') || ' ' ||
    coalesce(NEW.lcn,'') || ' ' || coalesce(NEW.call_type,'') || ' ' ||
    coalesce(NEW.transcript,'') || ' ' || coalesce(NEW.notes,''));
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS calls_search_document_trigger ON calls;
CREATE TRIGGER calls_search_document_trigger BEFORE INSERT OR UPDATE OF system_id,system_name,site_id,site_name,receiver_id,talkgroup_id,talkgroup_name,radio_id,radio_name,frequency,lcn,call_type,transcript,notes ON calls FOR EACH ROW EXECUTE FUNCTION calls_search_document_refresh();
UPDATE calls SET search_document = to_tsvector('simple'::regconfig,
  coalesce(system_id,'') || ' ' || coalesce(system_name,'') || ' ' || coalesce(site_id,'') || ' ' || coalesce(site_name,'') || ' ' || coalesce(receiver_id,'') || ' ' || coalesce(talkgroup_id,'') || ' ' || coalesce(talkgroup_name,'') || ' ' || coalesce(radio_id,'') || ' ' || coalesce(radio_name,'') || ' ' || coalesce(frequency,'') || ' ' || coalesce(lcn,'') || ' ' || coalesce(call_type,'') || ' ' || coalesce(transcript,'') || ' ' || coalesce(notes,''));
CREATE INDEX IF NOT EXISTS calls_text_search_idx ON calls USING gin (search_document);
