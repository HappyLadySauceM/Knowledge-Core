import type { Metadata } from "next";
import { SiteHeader } from "@/components/public/site-header";
import { SiteFooter } from "@/components/public/site-footer";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Knowledge Core",
    template: "%s | Knowledge Core",
  },
  description: "一个安静、可检索、持续生长的个人知识库。",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body>
        <SiteHeader />
        {children}
        <SiteFooter />
      </body>
    </html>
  );
}
