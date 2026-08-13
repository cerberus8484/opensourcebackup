import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

const BASE = import.meta.env.VITE_API_URL || ''

interface AuthState {
  authenticated: boolean
  email:         string
  role:          string
  loading:       boolean
}

const AuthContext = createContext<AuthState>({ authenticated: false, email: '', role: '', loading: true })

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ authenticated: false, email: '', role: '', loading: true })

  useEffect(() => {
    fetch(`${BASE}/auth/me`)
      .then(r => r.ok ? r.json() : { authenticated: false })
      .then(d => setState({ authenticated: !!d.authenticated, email: d.email ?? '', role: d.role ?? '', loading: false }))
      .catch(() => setState({ authenticated: false, email: '', role: '', loading: false }))
  }, [])

  return <AuthContext.Provider value={state}>{children}</AuthContext.Provider>
}

// The hook is intentionally colocated with its provider; this module is not hot-reloaded as a component boundary.
// eslint-disable-next-line react-refresh/only-export-components
export function useAuth() { return useContext(AuthContext) }
