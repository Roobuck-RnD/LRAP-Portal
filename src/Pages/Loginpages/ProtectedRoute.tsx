import { Navigate } from 'react-router'

export default function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const isLoggedIn = sessionStorage.getItem('isLoggedIn') === 'true'
  return isLoggedIn ? <>{children}</> : <Navigate to="/login" replace />
}
