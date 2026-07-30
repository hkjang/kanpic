import { useQuery } from '@tanstack/react-query'
import { api } from './lib/api'
import type { AuthConfig, BuildInfo, Session } from './types'
import { LoginPage } from './pages/LoginPage'
import { HomePage } from './pages/HomePage'
import { EditorPage } from './pages/EditorPage'
import { AdminPage } from './pages/AdminPage'
import { PreferencesPage } from './pages/PreferencesPage'

export function App() {
  const path = window.location.pathname
  const build = useQuery({ queryKey:['version'], queryFn:()=>api<BuildInfo>('/api/v1/version') })
  const auth = useQuery({ queryKey:['auth-config'], queryFn:()=>api<AuthConfig>('/api/v1/auth/config') })
  const authRequired = auth.data?.oidc_enabled === true || auth.data?.bootstrap_login_enabled === true
  const session = useQuery({ queryKey:['session'], queryFn:()=>api<Session>('/api/v1/session'), enabled:authRequired })
  const requestedReturnTo = path === '/login' ? new URLSearchParams(location.search).get('return_to') || '/' : `${location.pathname}${location.search}`
  const returnTo = requestedReturnTo.startsWith('/') && !requestedReturnTo.startsWith('//') ? requestedReturnTo : '/'

  if (path === '/login') return <LoginPage build={build.data} auth={auth.data} returnTo={returnTo} />
  if (authRequired && session.isFetched && !session.data?.authenticated) return <LoginPage build={build.data} auth={auth.data} returnTo={returnTo} />
  if (path.startsWith('/admin')) return <AdminPage build={build.data} session={session.data} />
  if (path.startsWith('/preferences')) return <PreferencesPage build={build.data} session={session.data} />
  const workbookMatch = path.match(/^\/workbooks\/([^/]+)/)
  if (workbookMatch) return <EditorPage workbookId={workbookMatch[1]} build={build.data} session={session.data} />
  return <HomePage build={build.data} session={session.data} />
}
