-- Full-text search foundation (Part 1 of the search/filtering/enrichment
-- batch): a `search_tsv tsvector` column on each of the five entities
-- users actually search for, auto-maintained via a BEFORE INSERT OR
-- UPDATE trigger (not a generated column - see docs/DEVELOPMENT.md's own
-- "why a trigger" writeup for this batch, the short version being that a
-- couple of these columns need data that isn't a plain function of the
-- row's own columns: workflow_instances.search_tsv needs a lookup into
-- workflow_states for the current state's key/name, which a generated
-- column's expression cannot do - Postgres generated columns are
-- restricted to functions of the SAME row's own columns, no subqueries
-- allowed). Every table here already has RLS from its own original
-- migration; adding a plain column changes nothing about that - no policy
-- updates needed.
--
-- 'simple' text search config throughout (never 'english' or any other
-- language-specific config): no stemming, so it matches substrings/words
-- consistently across every language a firm's data might be in, including
-- Turkish - the deployment context this design brief calls out
-- explicitly. Queried with plainto_tsquery('simple', ...), never
-- websearch_to_tsquery/to_tsquery (see this batch's own scope boundary:
-- those need more user-input escaping care and expose query syntax not
-- worth the complexity yet).
--
-- normalize_search_text: Postgres's own text search parser recognizes an
-- email address or bare "host.name" string as ONE atomic "email"/"host"
-- lexeme (ts_debug confirms this - unlike a hyphenated word, which the
-- parser already splits into both the whole compound AND its parts), so
-- 'jane@acme-example.com' indexes as a single indivisible token and a
-- search for just "acme-example.com" (a customer/supplier email DOMAIN,
-- this batch's own required test case) would never match it. Replacing
-- '@'/'.' with spaces before indexing - and applying that exact same
-- normalization to the QUERY text at read time (see each package's own
-- ...WithTSVFilter query below) - forces the parser to tokenize the local
-- part, each domain label, and the TLD as separate, matchable words
-- instead. IMMUTABLE so it can appear in an index expression if ever
-- needed later; harmless on non-email text (a product name/SKU containing
-- '.' just gets a harmless extra space).
CREATE FUNCTION normalize_search_text(text) RETURNS text
    LANGUAGE sql IMMUTABLE AS $$
    SELECT regexp_replace(COALESCE($1, ''), '[@.]', ' ', 'g');
$$;

-- workflow_instances: indexes the current state's key/name plus every
-- string leaf of payload (arbitrary per-definition JSON -
-- jsonb_path_query_array(payload, '$.**') walks every node recursively,
-- regardless of nesting depth, and the jsonb_typeof(elem) = 'string'
-- filter keeps only the string leaves, exactly as this batch's own design
-- brief asks for - not just top-level keys).
ALTER TABLE workflow_instances ADD COLUMN search_tsv tsvector;

CREATE OR REPLACE FUNCTION workflow_instances_search_tsv_update() RETURNS trigger AS $$
DECLARE
    state_key text;
    state_name text;
BEGIN
    SELECT key, name INTO state_key, state_name
    FROM workflow_states WHERE id = NEW.current_state_id;

    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(state_key, '') || ' ' || COALESCE(state_name, '') || ' ' ||
        COALESCE((
            SELECT string_agg(elem #>> '{}', ' ')
            FROM jsonb_array_elements(jsonb_path_query_array(NEW.payload, '$.**')) AS elem
            WHERE jsonb_typeof(elem) = 'string'
        ), '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER workflow_instances_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON workflow_instances
    FOR EACH ROW EXECUTE FUNCTION workflow_instances_search_tsv_update();

-- Backfill: the trigger above only fires on future inserts/updates: rows
-- that already existed before this migration ran need search_tsv computed
-- once here, by hand, with the identical formula the trigger uses (small,
-- deliberate duplication - a backfill UPDATE and a trigger body can't
-- share code in plain SQL, and this runs exactly once per migration).
UPDATE workflow_instances wi
SET search_tsv = to_tsvector('simple', normalize_search_text(
    COALESCE(ws.key, '') || ' ' || COALESCE(ws.name, '') || ' ' ||
    COALESCE((
        SELECT string_agg(elem #>> '{}', ' ')
        FROM jsonb_array_elements(jsonb_path_query_array(wi.payload, '$.**')) AS elem
        WHERE jsonb_typeof(elem) = 'string'
    ), '')
))
FROM workflow_states ws
WHERE ws.id = wi.current_state_id;

CREATE INDEX workflow_instances_search_tsv_idx ON workflow_instances USING GIN (search_tsv);

-- products: name, sku, description, category.
ALTER TABLE products ADD COLUMN search_tsv tsvector;

CREATE OR REPLACE FUNCTION products_search_tsv_update() RETURNS trigger AS $$
BEGIN
    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(NEW.name, '') || ' ' || COALESCE(NEW.sku, '') || ' ' ||
        COALESCE(NEW.description, '') || ' ' || COALESCE(NEW.category, '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER products_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION products_search_tsv_update();

UPDATE products SET search_tsv = to_tsvector('simple', normalize_search_text(
    COALESCE(name, '') || ' ' || COALESCE(sku, '') || ' ' ||
    COALESCE(description, '') || ' ' || COALESCE(category, '')
));

CREATE INDEX products_search_tsv_idx ON products USING GIN (search_tsv);

-- customers: name, email, phone.
ALTER TABLE customers ADD COLUMN search_tsv tsvector;

CREATE OR REPLACE FUNCTION customers_search_tsv_update() RETURNS trigger AS $$
BEGIN
    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(NEW.name, '') || ' ' || COALESCE(NEW.email, '') || ' ' || COALESCE(NEW.phone, '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER customers_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION customers_search_tsv_update();

UPDATE customers SET search_tsv = to_tsvector('simple', normalize_search_text(
    COALESCE(name, '') || ' ' || COALESCE(email, '') || ' ' || COALESCE(phone, '')
));

CREATE INDEX customers_search_tsv_idx ON customers USING GIN (search_tsv);

-- people: full_name, email, phone.
ALTER TABLE people ADD COLUMN search_tsv tsvector;

CREATE OR REPLACE FUNCTION people_search_tsv_update() RETURNS trigger AS $$
BEGIN
    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(NEW.full_name, '') || ' ' || COALESCE(NEW.email, '') || ' ' || COALESCE(NEW.phone, '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER people_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON people
    FOR EACH ROW EXECUTE FUNCTION people_search_tsv_update();

UPDATE people SET search_tsv = to_tsvector('simple', normalize_search_text(
    COALESCE(full_name, '') || ' ' || COALESCE(email, '') || ' ' || COALESCE(phone, '')
));

CREATE INDEX people_search_tsv_idx ON people USING GIN (search_tsv);

-- suppliers: name, contact_name, email.
ALTER TABLE suppliers ADD COLUMN search_tsv tsvector;

CREATE OR REPLACE FUNCTION suppliers_search_tsv_update() RETURNS trigger AS $$
BEGIN
    NEW.search_tsv := to_tsvector('simple', normalize_search_text(
        COALESCE(NEW.name, '') || ' ' || COALESCE(NEW.contact_name, '') || ' ' || COALESCE(NEW.email, '')
    ));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER suppliers_search_tsv_trigger
    BEFORE INSERT OR UPDATE ON suppliers
    FOR EACH ROW EXECUTE FUNCTION suppliers_search_tsv_update();

UPDATE suppliers SET search_tsv = to_tsvector('simple', normalize_search_text(
    COALESCE(name, '') || ' ' || COALESCE(contact_name, '') || ' ' || COALESCE(email, '')
));

CREATE INDEX suppliers_search_tsv_idx ON suppliers USING GIN (search_tsv);
