import { useEffect, useState } from 'react'
import { getProfile } from './api/task-api'
import './App.css'
import { UserProfile } from './components/user-profile'
import { TaskWorkspace } from './components/task-workspace'

function App() {
  const [displayName, setDisplayName] = useState('Загрузка профиля…')
  useEffect(() => {
    const controller = new AbortController()
    getProfile(controller.signal)
      .then((data) => { if (!controller.signal.aborted) setDisplayName(data.profile.display_name) })
      .catch(() => { if (!controller.signal.aborted) setDisplayName('Профиль недоступен') })
    return () => controller.abort()
  }, [])
  return (
    <div className="app">
      <header className="app-header">
        <div className="container app-header__inner">
          <div className="brand">
            <span className="brand__mark" aria-hidden="true">
              <span />
              <span />
              <span />
              <span />
            </span>
            <span>AI-buddy</span>
          </div>
          <UserProfile displayName={displayName} />
        </div>
      </header>

      <main className="container app-main">
        <h1>Разбор рабочей задачи</h1>
        <TaskWorkspace />
      </main>
    </div>
  )
}

export default App
