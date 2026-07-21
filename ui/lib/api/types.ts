export type Category = {
  id: number;
  name: string;
  slug: string;
  path: string;
  parent_id: number;
  sort: number;
  document_count: number;
  children?: Category[];
};

export type Tag = {
  id: number;
  name: string;
  slug: string;
  document_count: number;
  created_at?: string;
  updated_at?: string;
};

export type DocumentTag = Pick<Tag, "id" | "name" | "slug">;

export type DocumentCategory = Pick<Category, "id" | "name" | "slug" | "path">;

export type DocumentBlock = {
  block_id: string;
  parent_id: string;
  position_key: string;
  type: string;
  content_json: string;
  text_content: string;
  version: number;
  updated_by: number;
  updated_at: string;
};

export type DocumentBlockInput = {
  block_id: string;
  parent_id: string;
  position_key: string;
  type: string;
  content_json: string;
  text_content: string;
};

export type Document = {
  id: number;
  slug: string;
  title: string;
  summary: string;
  content?: string;
  blocks?: DocumentBlock[];
  category_id: number;
  category?: DocumentCategory;
  tags: DocumentTag[];
  source: string;
  status: "draft" | "published";
  confidence: number;
  word_count: number;
  cover_url: string;
  author_id: number;
  current_version: number;
  created_at: string;
  updated_at: string;
  published_at?: string;
};

export type ListResponse<T> = {
  items: T[];
  total?: number;
  page?: number;
  page_size?: number;
};

export type User = {
  id: number;
  username: string;
  email?: string;
  avatar: string;
  bio: string;
  role: "admin" | "user";
  status: "active" | "disabled";
  created_at: string;
  updated_at: string;
};

export type TokenResponse = {
  access_token: string;
  token_type: string;
  expires_in: number;
  refresh_token: string;
  scope: string;
  user: User;
};

export type Asset = {
  id: number;
  url: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  sha256: string;
  status: string;
  created_by: number;
  created_at: string;
  updated_at: string;
};

export type ApiEnvelope<T> = {
  code: number;
  message: string;
  data: T;
};
