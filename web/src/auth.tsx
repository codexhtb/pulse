import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, setCSRFToken, type AuthSession } from './api'

interface AuthContextValue {
  loading: boolean
  session: AuthSession | null
  login: (username: string, password: string) => Promise<AuthSession>
  logout: () => Promise<void>
  refresh: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [loading, setLoading] = useState(true)
  const [session, setSession] = useState<AuthSession | null>(null)

  const acceptSession = useCallback((next: AuthSession | null) => {
    setCSRFToken(next?.csrf_token ?? '')
    setSession(next)
  }, [])

  const refresh = useCallback(async () => {
    try {
      acceptSession(await api.authSession())
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) acceptSession(null)
      else throw error
    } finally {
      setLoading(false)
    }
  }, [acceptSession])

  useEffect(() => {
    void refresh()
    const unauthorized = () => acceptSession(null)
    window.addEventListener('pulse:unauthorized', unauthorized)
    return () => window.removeEventListener('pulse:unauthorized', unauthorized)
  }, [acceptSession, refresh])

  const value = useMemo<AuthContextValue>(() => ({
    loading,
    session,
    refresh,
    login: async (username, password) => {
      const next = await api.login(username, password)
      acceptSession(next)
      return next
    },
    logout: async () => {
      try { await api.logout() } finally { acceptSession(null) }
    },
  }), [acceptSession, loading, refresh, session])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAuth() {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside AuthProvider')
  return value
}
