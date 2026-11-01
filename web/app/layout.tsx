import "./globals.css";

export const metadata = {
  title: "AI4ALL · Certificate Mailer",
  description: "Bulk certificate delivery workspace"
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return <html lang="en"><body>{children}</body></html>;
}
