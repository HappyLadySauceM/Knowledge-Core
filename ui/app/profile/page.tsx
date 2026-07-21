import type { Metadata } from "next";
import { ProfileSettings } from "@/components/auth/profile-settings";

export const metadata: Metadata = { title: "个人资料" };

export default function ProfilePage() {
  return (
    <main className="mx-auto max-w-5xl px-5 py-12 lg:px-8">
      <header className="mb-10 border-b border-[var(--border)] pb-8"><p className="text-sm font-medium text-[var(--brand)]">Account</p><h1 className="mt-2 text-3xl font-semibold">个人资料</h1></header>
      <ProfileSettings />
    </main>
  );
}
