import type { Category, Document, ListResponse, Tag } from "./types";

const now = new Date("2026-06-25T09:00:00+08:00").toISOString();

export const fallbackDocuments: Document[] = [
  {
    id: 1,
    slug: "deep-learning-notes",
    title: "深度学习入门笔记",
    summary: "从神经网络基础概念讲起，涵盖前向传播、反向传播、损失函数与优化算法的核心原理。",
    content: "# 深度学习入门笔记\n\n深度学习的核心是用多层参数化函数逼近复杂关系。本文记录从张量到训练循环的关键概念。\n\n## 从一个神经元开始\n\n一个神经元接收输入，经过线性变换和激活函数得到输出。训练过程则通过损失函数衡量预测与目标之间的差异。\n\n```python\nloss.backward()\noptimizer.step()\n```\n\n## 小结\n\n先理解数据流，再理解优化过程，最后再选择模型结构。",
    category_id: 1,
    category: { id: 1, name: "技术", slug: "technology", path: "technology" },
    tags: [{ id: 1, name: "AI", slug: "ai" }, { id: 2, name: "深度学习", slug: "deep-learning" }],
    source: "原创",
    status: "published",
    confidence: 1,
    word_count: 3200,
    cover_url: "/images/cover-deep-learning.jpg",
    author_id: 1,
    current_version: 1,
    created_at: "2026-06-15T09:00:00+08:00",
    updated_at: now,
    published_at: "2026-06-15T09:00:00+08:00",
  },
  {
    id: 2,
    slug: "react-hooks-guide",
    title: "React Hooks 完全指南",
    summary: "系统梳理 useState、useEffect、useContext 等 Hooks 的使用场景与最佳实践。",
    content: "# React Hooks 完全指南\n\nHooks 让函数组件能够表达状态、同步外部系统，并共享可复用的逻辑。\n\n## 先从状态建模\n\n把状态拆成最小的可变单元，再用派生值组织界面。\n\n## Effect 的边界\n\nEffect 用于连接 React 与外部系统，不要把它当作计算派生值的工具。",
    category_id: 1,
    category: { id: 1, name: "技术", slug: "technology", path: "technology" },
    tags: [{ id: 3, name: "React", slug: "react" }, { id: 4, name: "TypeScript", slug: "typescript" }],
    source: "原创",
    status: "published",
    confidence: 1,
    word_count: 4100,
    cover_url: "/images/cover-react-hooks.jpg",
    author_id: 1,
    current_version: 1,
    created_at: "2026-06-20T09:00:00+08:00",
    updated_at: now,
    published_at: "2026-06-20T09:00:00+08:00",
  },
  {
    id: 3,
    slug: "go-concurrency-in-practice",
    title: "Go 并发编程实战",
    summary: "深入 Goroutine 调度模型、Channel 通信模式和 select 多路复用机制。",
    content: "# Go 并发编程实战\n\nGo 把并发原语做成了语言的一部分。本文从 goroutine 的生命周期开始，梳理可维护的并发模式。\n\n## Channel 是通信边界\n\n让数据流动起来，而不是让多个 goroutine 共享可变状态。",
    category_id: 1,
    category: { id: 1, name: "技术", slug: "technology", path: "technology" },
    tags: [{ id: 5, name: "Go", slug: "go" }, { id: 6, name: "并发", slug: "concurrency" }],
    source: "原创",
    status: "published",
    confidence: 1,
    word_count: 2800,
    cover_url: "/images/cover-go-concurrency.jpg",
    author_id: 1,
    current_version: 1,
    created_at: "2026-06-18T09:00:00+08:00",
    updated_at: now,
    published_at: "2026-06-18T09:00:00+08:00",
  },
  {
    id: 4,
    slug: "thinking-fast-and-slow-notes",
    title: "思考快与慢：读书笔记",
    summary: "Daniel Kahneman 关于系统 1 与系统 2 双系统思维的经典作品核心要点提炼。",
    content: "# 思考快与慢：读书笔记\n\n我们并不总是用同一种方式做决定。记录这本书，是为了在快速判断之外保留慢下来检查假设的机会。\n\n## 两套系统\n\n系统 1 快速、自动、依赖直觉；系统 2 缓慢、费力、需要集中注意力。",
    category_id: 2,
    category: { id: 2, name: "阅读", slug: "reading", path: "reading" },
    tags: [{ id: 7, name: "读书笔记", slug: "book-notes" }, { id: 8, name: "心理学", slug: "psychology" }],
    source: "阅读",
    status: "published",
    confidence: 1,
    word_count: 5400,
    cover_url: "/images/cover-thinking-fast-slow.jpg",
    author_id: 1,
    current_version: 1,
    created_at: "2026-06-10T09:00:00+08:00",
    updated_at: now,
    published_at: "2026-06-10T09:00:00+08:00",
  },
  {
    id: 5,
    slug: "pytorch-in-practice",
    title: "PyTorch 实战笔记",
    summary: "从张量操作到模型训练部署，一套完整的 PyTorch 工程化实践记录。",
    content: "# PyTorch 实战笔记\n\n模型代码只是训练系统的一部分。数据、实验记录、评估和部署同样决定结果是否可以复现。",
    category_id: 1,
    category: { id: 1, name: "技术", slug: "technology", path: "technology" },
    tags: [{ id: 1, name: "AI", slug: "ai" }, { id: 9, name: "PyTorch", slug: "pytorch" }],
    source: "原创",
    status: "published",
    confidence: 1,
    word_count: 6100,
    cover_url: "/images/cover-pytorch.jpg",
    author_id: 1,
    current_version: 1,
    created_at: "2026-06-25T09:00:00+08:00",
    updated_at: now,
    published_at: "2026-06-25T09:00:00+08:00",
  },
];

export const fallbackCategories: Category[] = [
  { id: 1, name: "技术", slug: "technology", path: "technology", parent_id: 0, sort: 1, document_count: 3 },
  { id: 2, name: "阅读", slug: "reading", path: "reading", parent_id: 0, sort: 2, document_count: 1 },
];

export const fallbackTags: Tag[] = Array.from(new Map(fallbackDocuments.flatMap((document) => document.tags).map((tag) => [tag.slug, { ...tag, document_count: 1 }])).values());

export function fallbackDocumentList(query: { q?: string; category?: string; tag?: string } = {}): ListResponse<Document> {
  const q = query.q?.toLocaleLowerCase().trim();
  const items = fallbackDocuments
    .filter((document) => {
      if (q && !`${document.title} ${document.summary} ${document.tags.map((tag) => tag.name).join(" ")}`.toLocaleLowerCase().includes(q)) return false;
      if (query.category && document.category?.slug !== query.category) return false;
      if (query.tag && !document.tags.some((tag) => tag.slug === query.tag)) return false;
      return true;
    })
    .sort((left, right) => {
      const byPublishedAt = new Date(right.published_at ?? 0).getTime() - new Date(left.published_at ?? 0).getTime();
      return byPublishedAt || right.id - left.id;
    });
  return { items, total: items.length, page: 1, page_size: 12 };
}
