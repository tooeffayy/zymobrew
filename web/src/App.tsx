import { Suspense, lazy } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";

import { AuthProvider, RequireAdmin, RequireAuth, useAuth } from "./auth";
import { AuthLayout } from "./components/AuthLayout";
import { Layout } from "./components/Layout";
import { NotificationsProvider } from "./notifications";
import { PreferencesProvider } from "./units";
import { AdminBackups } from "./pages/AdminBackups";
import { BatchCreate } from "./pages/BatchCreate";
import { BatchEdit } from "./pages/BatchEdit";

// BatchDetail pulls in Recharts (~150-200 KB raw). Splitting it keeps that
// out of the main bundle until a user actually opens a batch.
const BatchDetail = lazy(() =>
  import("./pages/BatchDetail").then((m) => ({ default: m.BatchDetail })),
);
import { Batches } from "./pages/Batches";
import { Calculators } from "./pages/Calculators";
import { EmailCancel, EmailConfirm } from "./pages/EmailChange";
import { Inventory } from "./pages/Inventory";
import { Login } from "./pages/Login";
import { Me } from "./pages/Me";
import { Notifications } from "./pages/Notifications";
import { RecipeCreate } from "./pages/RecipeCreate";
import { RecipeDetail } from "./pages/RecipeDetail";
import { RecipeEdit } from "./pages/RecipeEdit";
import { Recipes } from "./pages/Recipes";
import { Register } from "./pages/Register";

// Already-authenticated users hitting /login or /register get bounced
// home — keeps the back button from leaving the user on a stale form.
function RedirectIfAuthed({ children }: { children: JSX.Element }) {
  const { state } = useAuth();
  if (state.status === "loading") return null;
  if (state.status === "authed") return <Navigate to="/" replace />;
  return children;
}

export function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <PreferencesProvider>
        <NotificationsProvider>
        <Suspense fallback={null}>
        <Routes>
          {/* Auth screens render bare — no header, no nav, just the card. */}
          <Route
            path="/login"
            element={
              <RedirectIfAuthed>
                <AuthLayout>
                  <Login />
                </AuthLayout>
              </RedirectIfAuthed>
            }
          />
          <Route
            path="/register"
            element={
              <RedirectIfAuthed>
                <AuthLayout>
                  <Register />
                </AuthLayout>
              </RedirectIfAuthed>
            }
          />
          {/* Email-change landing pages render bare and work without a
              session — the token in the query string is the capability. */}
          <Route path="/email/confirm" element={<EmailConfirm />} />
          <Route path="/email/cancel" element={<EmailCancel />} />
          {/* Everything else gets the chrome. */}
          <Route
            path="/"
            element={
              <RequireAuth>
                <Layout>
                  <Recipes />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/calculators"
            element={
              <RequireAuth>
                <Layout>
                  <Calculators />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/recipes/new"
            element={
              <RequireAuth>
                <Layout>
                  <RecipeCreate />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/recipes/:id"
            element={
              <RequireAuth>
                <Layout>
                  <RecipeDetail />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/recipes/:id/edit"
            element={
              <RequireAuth>
                <Layout>
                  <RecipeEdit />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches"
            element={
              <RequireAuth>
                <Layout>
                  <Batches />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/new"
            element={
              <RequireAuth>
                <Layout>
                  <BatchCreate />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/:id"
            element={
              <RequireAuth>
                <Layout>
                  <BatchDetail />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/batches/:id/edit"
            element={
              <RequireAuth>
                <Layout>
                  <BatchEdit />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/inventory"
            element={
              <RequireAuth>
                <Layout>
                  <Inventory />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/me/*"
            element={
              <RequireAuth>
                <Layout>
                  <Me />
                </Layout>
              </RequireAuth>
            }
          />
          <Route
            path="/notifications"
            element={
              <RequireAuth>
                <Layout>
                  <Notifications />
                </Layout>
              </RequireAuth>
            }
          />
          {/* /admin lands on backups today; future admin sections add their
              own children. RequireAdmin gates on users.is_admin. */}
          <Route
            path="/admin"
            element={<Navigate to="/admin/backups" replace />}
          />
          <Route
            path="/admin/backups"
            element={
              <RequireAdmin>
                <Layout>
                  <AdminBackups />
                </Layout>
              </RequireAdmin>
            }
          />
          {/* Unknown SPA paths fall back home; the Go static handler
              already serves index.html for any non-/api/* path. */}
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
        </Suspense>
        </NotificationsProvider>
        </PreferencesProvider>
      </AuthProvider>
    </BrowserRouter>
  );
}
