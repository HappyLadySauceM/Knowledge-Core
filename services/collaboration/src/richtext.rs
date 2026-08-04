use serde_json::{Map, Number, Value, json};
use yrs::{
    Any, Doc, Out, ReadTxn, StateVector, Text, Transact, Update, Xml, XmlElementPrelim,
    XmlFragment, XmlOut,
    types::{AsPrelim, ToJson, xml::XmlIn},
    updates::decoder::Decode,
};

use crate::{
    domain::{DocumentId, Projection},
    error::{Result, ServiceError},
};

pub const FRAGMENT_NAME: &str = "default";
const MAX_DEPTH: usize = 64;
const MAX_NODES: usize = 100_000;

const ALLOWED_NODES: &[&str] = &[
    "paragraph",
    "heading",
    "bulletList",
    "orderedList",
    "listItem",
    "taskList",
    "taskItem",
    "blockquote",
    "codeBlock",
    "horizontalRule",
    "hardBreak",
    "text",
    "image",
    "attachment",
    "table",
    "tableRow",
    "tableHeader",
    "tableCell",
];

const ALLOWED_MARKS: &[&str] = &["bold", "italic", "strike", "underline", "code", "link"];

const BLOCK_NODES: &[&str] = &[
    "paragraph",
    "heading",
    "listItem",
    "taskItem",
    "blockquote",
    "codeBlock",
    "tableRow",
];

pub fn initial_state() -> Vec<u8> {
    let document = Doc::new();
    let fragment = document.get_or_insert_xml_fragment(FRAGMENT_NAME);
    fragment.push_back(
        &mut document.transact_mut(),
        XmlElementPrelim::empty("paragraph"),
    );
    full_state(&document)
}

/// Decodes and validates a complete persisted Yrs state.
///
/// # Errors
///
/// Returns an error when the state is not a valid Yrs update or violates the rich-text schema.
pub fn document_from_state(state: &[u8]) -> Result<Doc> {
    let document = Doc::new();
    let update = Update::decode_v1(state).map_err(|error| {
        ServiceError::invalid_input("persisted collaborative state is invalid").with_source(error)
    })?;
    document
        .transact_mut()
        .apply_update(update)
        .map_err(|error| {
            ServiceError::invalid_input("persisted collaborative state is invalid")
                .with_source(error)
        })?;
    projection_from_document(&document)?;
    Ok(document)
}

pub fn full_state(document: &Doc) -> Vec<u8> {
    document
        .transact()
        .encode_state_as_update_v1(&StateVector::default())
}

/// Replays persisted updates over a snapshot and returns a validated full state.
///
/// # Errors
///
/// Returns an internal error for corrupt persisted updates and an input error for invalid state.
pub fn merge_updates(state: &[u8], updates: &[Vec<u8>]) -> Result<Vec<u8>> {
    let document = document_from_state(state)?;
    for bytes in updates {
        let update = Update::decode_v1(bytes).map_err(|error| {
            ServiceError::internal(anyhow::anyhow!(error).context("decode persisted Yrs update"))
        })?;
        document
            .transact_mut()
            .apply_update(update)
            .map_err(|error| {
                ServiceError::internal(anyhow::anyhow!(error).context("apply persisted Yrs update"))
            })?;
    }
    projection_from_document(&document)?;
    Ok(full_state(&document))
}

/// Applies an untrusted update to a detached candidate document and validates all size boundaries.
///
/// # Errors
///
/// Returns an invalid-input error for malformed, oversized, or schema-invalid updates.
pub fn candidate_from_update(
    current_state: &[u8],
    update: &[u8],
    maximum_update_bytes: usize,
    maximum_document_bytes: usize,
) -> Result<Option<(Doc, Projection)>> {
    if update.is_empty() || update.len() > maximum_update_bytes {
        return Err(ServiceError::invalid_input(
            "collaboration update exceeds the configured size boundary",
        ));
    }
    let candidate = document_from_state(current_state)?;
    let before = candidate.transact().state_vector();
    let decoded = Update::decode_v1(update).map_err(|error| {
        ServiceError::invalid_input("collaboration update is invalid").with_source(error)
    })?;
    candidate
        .transact_mut()
        .apply_update(decoded)
        .map_err(|error| {
            ServiceError::invalid_input("collaboration update is invalid").with_source(error)
        })?;
    if candidate.transact().state_vector() == before {
        return Ok(None);
    }
    if full_state(&candidate).len() > maximum_document_bytes {
        return Err(ServiceError::invalid_input(
            "collaborative document exceeds the configured size boundary",
        ));
    }
    let projection = projection_from_document(&candidate)?;
    Ok(Some((candidate, projection)))
}

/// Builds a validated `ProseMirror` projection from a persisted Yrs state.
///
/// # Errors
///
/// Returns an error when the state or projected rich text is invalid.
pub fn projection_from_state(state: &[u8]) -> Result<Projection> {
    projection_from_document(&document_from_state(state)?)
}

/// Builds a validated `ProseMirror` projection from a Yrs document.
///
/// # Errors
///
/// Returns an invalid-input error when XML content cannot be represented by the supported schema.
pub fn projection_from_document(document: &Doc) -> Result<Projection> {
    let fragment = document.get_or_insert_xml_fragment(FRAGMENT_NAME);
    let transaction = document.transact();
    let mut content = Vec::new();
    let mut count = 0;
    for child in fragment.children(&transaction) {
        content.extend(serialize_xml(child, &transaction, 1, &mut count)?);
    }
    let value = json!({ "type": "doc", "content": content });
    validate_rich_text(&value)?;
    Ok(Projection {
        plain_text: extract_plain_text(&value),
        content: value,
    })
}

/// Produces a forward Yrs update that changes `current` to the target version's visible content.
///
/// # Errors
///
/// Returns an error when the target state is invalid or contains unsupported XML content.
pub fn restore_state(current: &Doc, target_state: &[u8]) -> Result<(Vec<u8>, Vec<u8>, Projection)> {
    let target = document_from_state(target_state)?;
    let target_fragment = target.get_or_insert_xml_fragment(FRAGMENT_NAME);
    let target_transaction = target.transact();
    let children = target_fragment
        .children(&target_transaction)
        .map(|child| match child {
            XmlOut::Element(value) => Ok(XmlIn::Element(value.as_prelim(&target_transaction))),
            XmlOut::Text(value) => Ok(XmlIn::Text(value.as_prelim(&target_transaction))),
            XmlOut::Fragment(_) => Err(ServiceError::invalid_input(
                "version contains an unsupported collaborative XML node",
            )),
        })
        .collect::<Result<Vec<_>>>()?;
    drop(target_transaction);

    let before = current.transact().state_vector();
    let fragment = current.get_or_insert_xml_fragment(FRAGMENT_NAME);
    let mut transaction = current.transact_mut();
    let length = fragment.len(&transaction);
    if length > 0 {
        fragment.remove_range(&mut transaction, 0, length);
    }
    for child in children {
        fragment.push_back(&mut transaction, child);
    }
    drop(transaction);
    let projection = projection_from_document(current)?;
    let update = current.transact().encode_state_as_update_v1(&before);
    let state = full_state(current);
    Ok((update, state, projection))
}

fn serialize_xml<T: ReadTxn>(
    value: XmlOut,
    transaction: &T,
    depth: usize,
    count: &mut usize,
) -> Result<Vec<Value>> {
    if depth > MAX_DEPTH {
        return Err(ServiceError::invalid_input(
            "content exceeds the maximum nesting depth",
        ));
    }
    match value {
        XmlOut::Element(element) => {
            increment_node_count(count)?;
            let mut node = Map::new();
            node.insert("type".to_owned(), Value::String(element.tag().to_string()));
            let attributes = element
                .attributes(transaction)
                .map(|(name, value)| Ok((name.to_owned(), out_to_json(value, transaction)?)))
                .collect::<Result<Map<String, Value>>>()?;
            if !attributes.is_empty() {
                node.insert("attrs".to_owned(), Value::Object(attributes));
            }
            let mut children = Vec::new();
            for child in element.children(transaction) {
                children.extend(serialize_xml(child, transaction, depth + 1, count)?);
            }
            if !children.is_empty() {
                node.insert("content".to_owned(), Value::Array(children));
            }
            Ok(vec![Value::Object(node)])
        }
        XmlOut::Text(text) => text
            .diff(transaction, |_| ())
            .into_iter()
            .map(|chunk| {
                increment_node_count(count)?;
                let Out::Any(Any::String(value)) = chunk.insert else {
                    return Err(ServiceError::invalid_input(
                        "collaborative text contains an unsupported embedded value",
                    ));
                };
                let mut node = Map::new();
                node.insert("type".to_owned(), Value::String("text".to_owned()));
                node.insert("text".to_owned(), Value::String(value.to_string()));
                if let Some(attributes) = chunk.attributes {
                    let marks = attributes
                        .into_iter()
                        .map(|(name, attributes)| {
                            let mut mark = Map::new();
                            mark.insert(
                                "type".to_owned(),
                                Value::String(mark_name(name.as_ref()).to_owned()),
                            );
                            let attributes = any_to_json(attributes)?;
                            if attributes
                                .as_object()
                                .is_some_and(|value| !value.is_empty())
                            {
                                mark.insert("attrs".to_owned(), attributes);
                            }
                            Ok(Value::Object(mark))
                        })
                        .collect::<Result<Vec<_>>>()?;
                    if !marks.is_empty() {
                        node.insert("marks".to_owned(), Value::Array(marks));
                    }
                }
                Ok(Value::Object(node))
            })
            .collect(),
        XmlOut::Fragment(_) => Err(ServiceError::invalid_input(
            "collaborative document contains a nested XML fragment",
        )),
    }
}

fn increment_node_count(count: &mut usize) -> Result<()> {
    *count = count.saturating_add(1);
    if *count > MAX_NODES {
        return Err(ServiceError::invalid_input(
            "content contains too many nodes",
        ));
    }
    Ok(())
}

fn out_to_json<T: ReadTxn>(value: Out, transaction: &T) -> Result<Value> {
    match value {
        Out::Any(value) => any_to_json(value),
        other => any_to_json(other.to_json(transaction)),
    }
}

fn any_to_json(value: Any) -> Result<Value> {
    match value {
        Any::Null => Ok(Value::Null),
        Any::Undefined | Any::Buffer(_) => Err(ServiceError::invalid_input(
            "collaborative attributes contain a non-JSON value",
        )),
        Any::Bool(value) => Ok(Value::Bool(value)),
        Any::Number(value) => canonical_json_number(value),
        Any::BigInt(value) => Ok(Value::Number(Number::from(value))),
        Any::String(value) => Ok(Value::String(value.to_string())),
        Any::Array(values) => values
            .iter()
            .cloned()
            .map(any_to_json)
            .collect::<Result<Vec<_>>>()
            .map(Value::Array),
        Any::Map(values) => values
            .iter()
            .map(|(key, value)| Ok((key.clone(), any_to_json(value.clone())?)))
            .collect::<Result<Map<_, _>>>()
            .map(Value::Object),
    }
}

fn canonical_json_number(value: f64) -> Result<Value> {
    const I64_LOWER_INCLUSIVE: f64 = -9_223_372_036_854_775_808.0;
    const I64_UPPER_EXCLUSIVE: f64 = 9_223_372_036_854_775_808.0;
    if value.fract() == 0.0 && (I64_LOWER_INCLUSIVE..I64_UPPER_EXCLUSIVE).contains(&value) {
        #[expect(
            clippy::cast_possible_truncation,
            reason = "the finite integral value is bounded to the complete i64 range above"
        )]
        return Ok(Value::Number(Number::from(value as i64)));
    }
    Number::from_f64(value)
        .map(Value::Number)
        .ok_or_else(|| ServiceError::invalid_input("collaborative attribute number is invalid"))
}

fn mark_name(value: &str) -> &str {
    let Some((name, suffix)) = value.rsplit_once("--") else {
        return value;
    };
    if suffix.len() == 8
        && suffix
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || matches!(value, b'+' | b'/' | b'='))
    {
        name
    } else {
        value
    }
}

/// Validates a `ProseMirror` JSON document against the bounded Collaboration schema.
///
/// # Errors
///
/// Returns an invalid-input error for unsupported nodes, marks, attributes, or size boundaries.
pub fn validate_rich_text(value: &Value) -> Result<()> {
    let object = value.as_object().ok_or_else(|| {
        ServiceError::invalid_input("content must be a ProseMirror doc with a content array")
    })?;
    if object
        .keys()
        .any(|key| !matches!(key.as_str(), "type" | "content"))
    {
        return Err(ServiceError::invalid_input(
            "content contains unsupported document fields",
        ));
    }
    if object.get("type").and_then(Value::as_str) != Some("doc")
        || object.get("content").and_then(Value::as_array).is_none()
    {
        return Err(ServiceError::invalid_input(
            "content must be a ProseMirror doc with a content array",
        ));
    }
    let content = object
        .get("content")
        .and_then(Value::as_array)
        .ok_or_else(|| {
            ServiceError::invalid_input("content must be a ProseMirror doc with a content array")
        })?;
    if content.is_empty() {
        return Err(ServiceError::invalid_input(
            "content must contain at least one block node",
        ));
    }
    let mut count = 0;
    for node in content {
        validate_node(node, None, 1, &mut count)?;
    }
    Ok(())
}

fn validate_node(
    value: &Value,
    parent_type: Option<&str>,
    depth: usize,
    count: &mut usize,
) -> Result<()> {
    let object = value.as_object().ok_or_else(|| {
        ServiceError::invalid_input("content contains an unsupported or deeply nested node")
    })?;
    if object.keys().any(|key| {
        !matches!(
            key.as_str(),
            "type" | "attrs" | "content" | "text" | "marks"
        )
    }) {
        return Err(ServiceError::invalid_input(
            "content contains unsupported node fields",
        ));
    }
    let node_type = object.get("type").and_then(Value::as_str).ok_or_else(|| {
        ServiceError::invalid_input("content contains an unsupported or deeply nested node")
    })?;
    if depth > MAX_DEPTH || !ALLOWED_NODES.contains(&node_type) {
        return Err(ServiceError::invalid_input(
            "content contains an unsupported or deeply nested node",
        ));
    }
    *count += 1;
    if *count > MAX_NODES {
        return Err(ServiceError::invalid_input(
            "content contains too many nodes",
        ));
    }
    let content = object.get("content");
    let marks = object.get("marks");
    let attributes = object.get("attrs");
    if content.is_some_and(|value| !value.is_array()) {
        return Err(ServiceError::invalid_input("node content must be an array"));
    }
    if marks.is_some_and(|value| !value.is_array()) {
        return Err(ServiceError::invalid_input("node marks must be an array"));
    }
    if node_type == "text" {
        if object
            .get("text")
            .and_then(Value::as_str)
            .is_none_or(str::is_empty)
            || content.is_some()
            || attributes.is_some()
        {
            return Err(ServiceError::invalid_input(
                "content contains an invalid text node",
            ));
        }
    } else {
        if object.contains_key("text") {
            return Err(ServiceError::invalid_input(
                "non-text nodes must not contain text",
            ));
        }
        if marks.is_some() {
            return Err(ServiceError::invalid_input(
                "marks are only valid on text nodes",
            ));
        }
    }
    if let Some(attributes) = attributes {
        validate_attributes(node_type, attributes)?;
    } else {
        validate_required_attributes(node_type, None)?;
    }
    if let Some(marks) = marks.and_then(Value::as_array) {
        for (index, mark) in marks.iter().enumerate() {
            let mark_type = validate_mark(mark)?;
            if marks[..index]
                .iter()
                .any(|previous| previous.get("type").and_then(Value::as_str) == Some(mark_type))
            {
                return Err(ServiceError::invalid_input(
                    "content contains duplicate marks",
                ));
            }
        }
        if marks.len() > 1
            && marks
                .iter()
                .any(|mark| mark.get("type").and_then(Value::as_str) == Some("code"))
        {
            return Err(ServiceError::invalid_input(
                "code marks cannot be combined with other marks",
            ));
        }
    }
    validate_content_shape(node_type, parent_type, content.and_then(Value::as_array))?;
    if let Some(content) = content.and_then(Value::as_array) {
        for child in content {
            validate_node(child, Some(node_type), depth + 1, count)?;
        }
    }
    Ok(())
}

fn validate_mark(value: &Value) -> Result<&str> {
    let object = value
        .as_object()
        .ok_or_else(|| ServiceError::invalid_input("content contains an unsupported mark"))?;
    if object
        .keys()
        .any(|key| !matches!(key.as_str(), "type" | "attrs"))
    {
        return Err(ServiceError::invalid_input(
            "content contains unsupported mark fields",
        ));
    }
    let mark_type = object
        .get("type")
        .and_then(Value::as_str)
        .filter(|value| ALLOWED_MARKS.contains(value))
        .ok_or_else(|| ServiceError::invalid_input("content contains an unsupported mark"))?;
    if let Some(attributes) = object.get("attrs") {
        validate_attributes(mark_type, attributes)?;
    } else {
        validate_required_attributes(mark_type, None)?;
    }
    Ok(mark_type)
}

fn validate_content_shape(
    node_type: &str,
    parent_type: Option<&str>,
    content: Option<&Vec<Value>>,
) -> Result<()> {
    let allowed_here = match parent_type {
        None => is_block_node(node_type),
        Some("paragraph" | "heading") => is_inline_node(node_type),
        Some("codeBlock") => node_type == "text",
        Some("bulletList" | "orderedList") => node_type == "listItem",
        Some("taskList") => node_type == "taskItem",
        Some("listItem" | "taskItem" | "blockquote" | "tableHeader" | "tableCell") => {
            is_block_node(node_type)
        }
        Some("table") => node_type == "tableRow",
        Some("tableRow") => matches!(node_type, "tableHeader" | "tableCell"),
        Some(_) => false,
    };
    if !allowed_here {
        return Err(ServiceError::invalid_input(
            "content contains an invalid node hierarchy",
        ));
    }

    let children = content.map_or(&[][..], Vec::as_slice);
    match node_type {
        "text" | "horizontalRule" | "hardBreak" | "image" | "attachment" => {
            if content.is_some() {
                return Err(ServiceError::invalid_input(
                    "leaf nodes must not contain a content field",
                ));
            }
        }
        "paragraph" | "heading" => {
            require_child_types(children, is_inline_node)?;
        }
        "codeBlock" => {
            require_child_types(children, |child| child == "text")?;
            if children.iter().any(|child| {
                child
                    .get("marks")
                    .and_then(Value::as_array)
                    .is_some_and(|marks| !marks.is_empty())
            }) {
                return Err(ServiceError::invalid_input(
                    "code blocks must not contain marked text",
                ));
            }
        }
        "bulletList" | "orderedList" => {
            require_non_empty_children(children, "lists")?;
            require_child_types(children, |child| child == "listItem")?;
        }
        "taskList" => {
            require_non_empty_children(children, "task lists")?;
            require_child_types(children, |child| child == "taskItem")?;
        }
        "listItem" | "taskItem" => {
            require_non_empty_children(children, "list items")?;
            if node_type_of(&children[0]) != Some("paragraph") {
                return Err(ServiceError::invalid_input(
                    "list items must start with a paragraph",
                ));
            }
            require_child_types(children, is_block_node)?;
        }
        "blockquote" => {
            require_non_empty_children(children, "blockquotes")?;
            require_child_types(children, is_block_node)?;
        }
        "table" => {
            require_non_empty_children(children, "tables")?;
            require_child_types(children, |child| child == "tableRow")?;
            let columns = table_row_columns(&children[0])?;
            if columns == 0 || columns > 100 {
                return Err(ServiceError::invalid_input(
                    "table rows must have the same bounded column count",
                ));
            }
            for row in &children[1..] {
                if table_row_columns(row)? != columns {
                    return Err(ServiceError::invalid_input(
                        "table rows must have the same bounded column count",
                    ));
                }
            }
        }
        "tableRow" => {
            require_non_empty_children(children, "table rows")?;
            require_child_types(children, |child| {
                matches!(child, "tableHeader" | "tableCell")
            })?;
        }
        "tableHeader" | "tableCell" => {
            require_non_empty_children(children, "table cells")?;
            require_child_types(children, is_block_node)?;
        }
        _ => {}
    }
    Ok(())
}

fn is_inline_node(node_type: &str) -> bool {
    matches!(node_type, "text" | "hardBreak")
}

fn is_block_node(node_type: &str) -> bool {
    matches!(
        node_type,
        "paragraph"
            | "heading"
            | "bulletList"
            | "orderedList"
            | "taskList"
            | "blockquote"
            | "codeBlock"
            | "horizontalRule"
            | "image"
            | "attachment"
            | "table"
    )
}

fn node_type_of(value: &Value) -> Option<&str> {
    value.get("type").and_then(Value::as_str)
}

fn table_row_columns(row: &Value) -> Result<usize> {
    let cells = row
        .get("content")
        .and_then(Value::as_array)
        .ok_or_else(|| ServiceError::invalid_input("table rows require cell content"))?;
    cells.iter().try_fold(0_usize, |columns, cell| {
        let colspan = cell
            .get("attrs")
            .and_then(Value::as_object)
            .and_then(|attributes| attributes.get("colspan"))
            .and_then(Value::as_u64)
            .unwrap_or(1);
        let colspan = usize::try_from(colspan).map_err(|error| {
            ServiceError::invalid_input("table colspan is invalid").with_source(error)
        })?;
        columns
            .checked_add(colspan)
            .ok_or_else(|| ServiceError::invalid_input("table column count overflow"))
    })
}

fn require_child_types(children: &[Value], allowed: impl Fn(&str) -> bool) -> Result<()> {
    if children
        .iter()
        .any(|child| node_type_of(child).is_none_or(|child| !allowed(child)))
    {
        return Err(ServiceError::invalid_input(
            "content contains an invalid node hierarchy",
        ));
    }
    Ok(())
}

fn require_non_empty_children(children: &[Value], parent: &str) -> Result<()> {
    if children.is_empty() {
        return Err(ServiceError::invalid_input(format!(
            "{parent} must contain at least one child node"
        )));
    }
    Ok(())
}

fn validate_attributes(node_type: &str, value: &Value) -> Result<()> {
    let attributes = value
        .as_object()
        .ok_or_else(|| ServiceError::invalid_input("content contains unsupported attributes"))?;
    let allowed = allowed_attributes(node_type);
    if attributes
        .keys()
        .any(|key| !allowed.contains(&key.as_str()))
    {
        return Err(ServiceError::invalid_input(
            "content contains unsupported attributes",
        ));
    }
    validate_required_attributes(node_type, Some(attributes))?;
    validate_list_and_heading_attributes(attributes)?;
    validate_text_attributes(attributes)?;
    validate_reference_attributes(attributes)?;
    validate_table_attributes(attributes)
}

fn validate_list_and_heading_attributes(attributes: &Map<String, Value>) -> Result<()> {
    if let Some(level) = attributes.get("level")
        && level.as_i64().is_none_or(|value| !(1..=6).contains(&value))
    {
        return Err(ServiceError::invalid_input(
            "heading level must be between 1 and 6",
        ));
    }
    if let Some(start) = attributes.get("start")
        && start
            .as_i64()
            .is_none_or(|value| value < 1 || i32::try_from(value).is_err())
    {
        return Err(ServiceError::invalid_input(
            "ordered list start must be positive",
        ));
    }
    if let Some(checked) = attributes.get("checked")
        && !checked.is_boolean()
    {
        return Err(ServiceError::invalid_input(
            "task item checked attribute must be boolean",
        ));
    }
    Ok(())
}

fn validate_text_attributes(attributes: &Map<String, Value>) -> Result<()> {
    if let Some(language) = attributes.get("language")
        && !valid_optional_string(language, 128)
    {
        return Err(ServiceError::invalid_input(
            "code block language is invalid",
        ));
    }
    for name in ["alt", "title"] {
        if let Some(value) = attributes.get(name)
            && !valid_optional_string(value, 1_024)
        {
            return Err(ServiceError::invalid_input(format!(
                "content {name} attribute is invalid"
            )));
        }
    }
    if let Some(alignment) = attributes.get("textAlign")
        && alignment
            .as_str()
            .is_none_or(|value| !["left", "center", "right", "justify"].contains(&value))
    {
        return Err(ServiceError::invalid_input(
            "content contains an invalid text alignment",
        ));
    }
    Ok(())
}

fn validate_reference_attributes(attributes: &Map<String, Value>) -> Result<()> {
    if let Some(attachment_id) = attributes.get("attachmentId") {
        let value = attachment_id
            .as_str()
            .ok_or_else(|| ServiceError::invalid_input("attachmentId must be a UUIDv7"))?;
        DocumentId::parse(value).map_err(|error| {
            ServiceError::invalid_input("attachmentId must be a UUIDv7").with_source(error)
        })?;
    }
    if let Some(href) = attributes.get("href")
        && href.as_str().is_none_or(|href| !valid_link(href))
    {
        return Err(ServiceError::invalid_input(
            "content contains an invalid link mark",
        ));
    }
    Ok(())
}

fn validate_table_attributes(attributes: &Map<String, Value>) -> Result<()> {
    for name in ["colspan", "rowspan"] {
        if let Some(value) = attributes.get(name)
            && value
                .as_i64()
                .is_none_or(|value| !(1..=100).contains(&value))
        {
            return Err(ServiceError::invalid_input(format!(
                "table {name} is out of range"
            )));
        }
    }
    if let Some(widths) = attributes.get("colwidth") {
        let valid = widths.is_null()
            || widths.as_array().is_some_and(|values| {
                let colspan = attributes
                    .get("colspan")
                    .and_then(Value::as_i64)
                    .unwrap_or(1);
                !values.is_empty()
                    && values.len() <= 100
                    && values.iter().all(|value| {
                        value
                            .as_i64()
                            .is_some_and(|value| value > 0 && i32::try_from(value).is_ok())
                    })
                    && usize::try_from(colspan) == Ok(values.len())
            });
        if !valid {
            return Err(ServiceError::invalid_input("table colwidth is invalid"));
        }
    }
    Ok(())
}

fn allowed_attributes(node_type: &str) -> &'static [&'static str] {
    match node_type {
        "paragraph" => &["textAlign"],
        "heading" => &["level", "textAlign"],
        "orderedList" => &["start"],
        "taskItem" => &["checked"],
        "codeBlock" => &["language"],
        "image" => &["attachmentId", "alt", "title"],
        "attachment" => &["attachmentId", "title"],
        "tableHeader" | "tableCell" => &["colspan", "rowspan", "colwidth"],
        "link" => &["href", "title"],
        _ => &[],
    }
}

fn validate_required_attributes(
    node_type: &str,
    attributes: Option<&Map<String, Value>>,
) -> Result<()> {
    let required = match node_type {
        "heading" => Some(("level", "heading nodes require a level")),
        "taskItem" => Some(("checked", "task items require checked state")),
        "image" | "attachment" => Some(("attachmentId", "attachment nodes require attachmentId")),
        "link" => Some(("href", "link marks require href")),
        _ => None,
    };
    if let Some((name, message)) = required
        && attributes.is_none_or(|attributes| !attributes.contains_key(name))
    {
        return Err(ServiceError::invalid_input(message));
    }
    Ok(())
}

fn valid_optional_string(value: &Value, maximum_bytes: usize) -> bool {
    value.is_null()
        || value
            .as_str()
            .is_some_and(|value| value.len() <= maximum_bytes)
}

fn valid_link(value: &str) -> bool {
    if value.len() > 2048 || value.contains(['\r', '\n']) {
        return false;
    }
    url::Url::parse(value)
        .ok()
        .is_some_and(|value| ["http", "https", "mailto"].contains(&value.scheme()))
}

fn extract_plain_text(root: &Value) -> String {
    fn visit(value: &Value, lines: &mut Vec<String>, current: &mut String) {
        let Some(object) = value.as_object() else {
            return;
        };
        let node_type = object
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default();
        if node_type == "text"
            && let Some(text) = object.get("text").and_then(Value::as_str)
        {
            current.push_str(text);
        }
        if node_type == "hardBreak" {
            current.push('\n');
        }
        if let Some(children) = object.get("content").and_then(Value::as_array) {
            for child in children {
                visit(child, lines, current);
            }
        }
        if BLOCK_NODES.contains(&node_type) && !current.trim().is_empty() {
            lines.push(current.trim().to_owned());
            current.clear();
        }
    }

    let mut lines = Vec::new();
    let mut current = String::new();
    visit(root, &mut lines, &mut current);
    if !current.trim().is_empty() {
        lines.push(current.trim().to_owned());
    }
    lines.join("\n")
}

#[cfg(test)]
mod tests {
    use yrs::{Doc, Transact, XmlElementPrelim, XmlFragment};

    use super::{
        MAX_DEPTH, canonical_json_number, initial_state, projection_from_document,
        projection_from_state, validate_rich_text,
    };

    #[test]
    fn canonical_empty_document_matches_y_prosemirror() {
        let projection = projection_from_state(&initial_state()).expect("valid initial state");
        assert_eq!(
            projection.content,
            serde_json::json!({"type":"doc","content":[{"type":"paragraph"}]})
        );
        assert!(projection.plain_text.is_empty());
    }

    #[test]
    fn canonicalizes_javascript_integer_attributes_for_typed_projection() {
        assert_eq!(
            canonical_json_number(2.0).expect("integral number"),
            serde_json::json!(2)
        );
        assert_eq!(
            canonical_json_number(1.5).expect("fractional number"),
            serde_json::json!(1.5)
        );
        assert!(canonical_json_number(f64::NAN).is_err());
    }

    #[test]
    fn projection_rejects_deep_xml_before_recursive_serialization() {
        let document = Doc::new();
        let fragment = document.get_or_insert_xml_fragment("default");
        let mut transaction = document.transact_mut();
        let mut parent = fragment.push_back(&mut transaction, XmlElementPrelim::empty("paragraph"));
        for _ in 0..MAX_DEPTH {
            parent = parent.push_back(&mut transaction, XmlElementPrelim::empty("paragraph"));
        }
        drop(transaction);

        let error = projection_from_document(&document).expect_err("deep XML must be rejected");
        assert_eq!(error.key(), "collaboration.invalid_input");
    }

    #[test]
    fn bounded_schema_accepts_every_supported_node_mark_and_attribute() {
        let attachment_id = "01890f47-76a8-7b1c-b4db-1d9d3906f73b";
        let document = serde_json::json!({
            "type": "doc",
            "content": [
                {"type":"paragraph","attrs":{"textAlign":"center"},"content":[
                    {"type":"text","text":"formatted","marks":[
                        {"type":"bold"}, {"type":"italic"}, {"type":"strike"},
                        {"type":"underline"},
                        {"type":"link","attrs":{"href":"https://example.com/path","title":null}}
                    ]},
                    {"type":"hardBreak"},
                    {"type":"text","text":"code","marks":[{"type":"code"}]}
                ]},
                {"type":"heading","attrs":{"level":2,"textAlign":"left"},"content":[
                    {"type":"text","text":"Heading"}
                ]},
                {"type":"bulletList","content":[{"type":"listItem","content":[
                    {"type":"paragraph","content":[{"type":"text","text":"Bullet"}]}
                ]}]},
                {"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[
                    {"type":"paragraph","content":[{"type":"text","text":"Ordered"}]}
                ]}]},
                {"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":false},"content":[
                    {"type":"paragraph","content":[{"type":"text","text":"Task"}]}
                ]}]},
                {"type":"blockquote","content":[
                    {"type":"paragraph","content":[{"type":"text","text":"Quote"}]}
                ]},
                {"type":"codeBlock","attrs":{"language":"rust"},"content":[
                    {"type":"text","text":"fn main() {}"}
                ]},
                {"type":"horizontalRule"},
                {"type":"image","attrs":{"attachmentId":attachment_id,"alt":"diagram","title":null}},
                {"type":"attachment","attrs":{"attachmentId":attachment_id,"title":"notes.txt"}},
                {"type":"table","content":[
                    {"type":"tableRow","content":[
                        {"type":"tableHeader","attrs":{"colspan":2,"rowspan":1,"colwidth":[120,120]},"content":[
                            {"type":"paragraph","content":[{"type":"text","text":"Header"}]}
                        ]}
                    ]},
                    {"type":"tableRow","content":[
                        {"type":"tableCell","attrs":{"colspan":1,"rowspan":1,"colwidth":null},"content":[
                            {"type":"paragraph","content":[{"type":"text","text":"Left"}]}
                        ]},
                        {"type":"tableCell","attrs":{"colspan":1,"rowspan":1,"colwidth":[120]},"content":[
                            {"type":"paragraph","content":[{"type":"text","text":"Right"}]}
                        ]}
                    ]}
                ]}
            ]
        });

        validate_rich_text(&document).expect("complete supported schema must be valid");
    }

    #[test]
    fn bounded_schema_rejects_cross_node_attributes_leaf_children_and_invalid_hierarchy() {
        let invalid_documents = [
            serde_json::json!({"type":"doc","content":[]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"paragraph","attrs":{"level":2}}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"horizontalRule","content":[]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"bulletList","content":[{"type":"paragraph"}]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"table","content":[{"type":"tableRow","content":[{"type":"paragraph"}]}]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"table","content":[
                    {"type":"tableRow","content":[
                        {"type":"tableCell","content":[{"type":"paragraph"}]}
                    ]},
                    {"type":"tableRow","content":[
                        {"type":"tableCell","content":[{"type":"paragraph"}]},
                        {"type":"tableCell","content":[{"type":"paragraph"}]}
                    ]}
                ]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"table","content":[{"type":"tableRow","content":[
                    {"type":"tableCell","attrs":{"colspan":2,"colwidth":[120]},"content":[{"type":"paragraph"}]}
                ]}]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"taskList","content":[{"type":"taskItem","content":[{"type":"paragraph"}]}]}
            ]}),
            serde_json::json!({"type":"doc","content":[{"type":"heading"}]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"paragraph","marks":[],"content":[{"type":"text","text":"x"}]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"code"},{"type":"bold"}]}]}
            ]}),
            serde_json::json!({"type":"doc","content":[
                {"type":"paragraph","unexpected":true}
            ]}),
        ];

        for document in invalid_documents {
            let error = validate_rich_text(&document).expect_err("schema violation must fail");
            assert_eq!(error.key(), "collaboration.invalid_input");
        }
    }
}
