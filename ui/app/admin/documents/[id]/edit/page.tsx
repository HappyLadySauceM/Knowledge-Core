import { DocumentEditor } from "@/components/admin/document-editor";

export default async function EditDocumentPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <DocumentEditor documentId={id} />;
}
