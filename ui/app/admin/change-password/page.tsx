import type { Metadata } from "next";
import { AdminChangePasswordForm } from "@/components/admin/change-password-form";

export const metadata: Metadata = { title: "修改初始密码" };

export default function AdminChangePasswordPage() {
  return (
    <div className="grid min-h-screen place-items-center bg-[var(--background)] px-5 py-12">
      <AdminChangePasswordForm />
    </div>
  );
}
