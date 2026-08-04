use base64::{Engine as _, engine::general_purpose::STANDARD};
use knowledge_core_collaboration::richtext;
use serde_json::Value;

const YRS_FIXTURE: &str = include_str!("../interop/fixtures/yrs-update-v1.json");
const YJS_FIXTURE: &str = include_str!("../interop/fixtures/yjs-update-v1.json");

#[test]
fn committed_yrs_update_v1_remains_a_valid_empty_document() {
    let fixture = parse_fixture(YRS_FIXTURE);
    let state = decode_state(&fixture);
    let projection = richtext::projection_from_state(&state).expect("Yrs fixture state");
    assert_eq!(
        projection.content,
        serde_json::json!({"type":"doc","content":[{"type":"paragraph"}]})
    );
}

#[test]
fn official_yprosemirror_update_v1_projects_complete_schema_through_yrs() {
    let fixture = parse_fixture(YJS_FIXTURE);
    let state = decode_state(&fixture);
    let projection = richtext::projection_from_state(&state).expect("Yjs state accepted by Yrs");

    assert_eq!(projection.content, fixture["projection"]);
    assert_eq!(projection.plain_text, fixture["plain_text"]);
}

#[test]
fn yrs_reencoding_preserves_yprosemirror_projection_truth() {
    let fixture = parse_fixture(YJS_FIXTURE);
    let state = decode_state(&fixture);
    let document = richtext::document_from_state(&state).expect("Yjs state accepted by Yrs");
    let reencoded = richtext::full_state(&document);
    let projection =
        richtext::projection_from_state(&reencoded).expect("Yrs update-v1 remains valid");

    assert_eq!(projection.content, fixture["projection"]);
    assert_eq!(projection.plain_text, fixture["plain_text"]);
}

fn parse_fixture(source: &str) -> Value {
    serde_json::from_str(source).expect("fixture JSON")
}

fn decode_state(fixture: &Value) -> Vec<u8> {
    STANDARD
        .decode(
            fixture["state_base64"]
                .as_str()
                .expect("base64 fixture state"),
        )
        .expect("base64 state")
}
