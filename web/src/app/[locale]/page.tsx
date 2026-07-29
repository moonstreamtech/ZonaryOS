import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { getTranslations, setRequestLocale } from "next-intl/server";
import { fetchMe } from "@/lib/me";

type PageProps = {
  params: Promise<{ locale: string }>;
};

async function fetchBackendStatus(): Promise<boolean> {
  const apiBase = process.env.ZONARYOS_API_BASE_URL ?? "http://localhost:8080";
  try {
    const res = await fetch(`${apiBase}/healthz`, { cache: "no-store" });
    return res.ok;
  } catch {
    return false;
  }
}

export default async function Home({ params }: PageProps) {
  const { locale } = await params;
  setRequestLocale(locale);
  const t = await getTranslations("Home");
  const tAuth = await getTranslations("Auth");
  const backendUp = await fetchBackendStatus();

  const sessionToken = (await cookies()).get("zonaryos_session")?.value;
  const me = sessionToken ? await fetchMe(sessionToken) : null;

  // Vision §3's wizard trigger: a signed-in user with zero firm
  // memberships is routed into the wizard instead of the (currently
  // still placeholder) main app below.
  if (me && me.firms.length === 0) {
    redirect(`/${locale}/wizard`);
  }

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-6 bg-zinc-50 px-6 py-24 text-center dark:bg-black">
      <h1 className="text-3xl font-semibold tracking-tight text-black dark:text-zinc-50">
        {t("title")}
      </h1>
      <p className="max-w-md text-lg text-zinc-600 dark:text-zinc-400">
        {t("subtitle")}
      </p>
      <p className="text-sm text-zinc-500 dark:text-zinc-500">
        {t("statusLabel")}:{" "}
        <span className={backendUp ? "text-green-600" : "text-red-600"}>
          {backendUp ? t("statusOk") : t("statusError")}
        </span>
      </p>

      {me ? (
        <div className="flex flex-col items-center gap-2 text-sm text-zinc-600 dark:text-zinc-400">
          <p>{tAuth("signedInAs", { name: me.displayName || me.email })}</p>
          {/* me.firms is always non-empty here: the zero-firm case
              redirected into the wizard above before this renders. */}
          <ul>
            {me.firms.map((firm) => (
              <li key={firm.firmId}>{firm.firmName}</li>
            ))}
          </ul>
          {/* eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/logout is a route handler, not a page: it must be a real navigation, not client-side routing */}
          <a
            href="/api/auth/logout"
            className="font-medium text-zinc-950 underline dark:text-zinc-50"
          >
            {tAuth("signOut")}
          </a>
        </div>
      ) : (
        // eslint-disable-next-line @next/next/no-html-link-for-pages -- /api/auth/login is a route handler, not a page
        <a
          href="/api/auth/login"
          className="rounded-full bg-foreground px-5 py-2 text-sm font-medium text-background transition-colors hover:bg-[#383838] dark:hover:bg-[#ccc]"
        >
          {tAuth("signIn")}
        </a>
      )}
    </main>
  );
}
