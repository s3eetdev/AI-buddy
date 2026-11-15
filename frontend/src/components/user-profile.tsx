import './user-profile.css'

type UserProfileProps = {
  displayName: string
  team?: string
}

export function UserProfile({ displayName, team }: UserProfileProps) {
  const initials = displayName.split(' ').slice(0, 2).map((word) => word.charAt(0)).join('').toUpperCase()

  return (
    <div className="user-profile">
      <span className="user-profile__avatar" aria-hidden="true">
        {initials}
      </span>
      <div className="user-profile__details">
        <p className="user-profile__name">{displayName}</p>
        {team && <p className="user-profile__team">{team}</p>}
      </div>
    </div>
  )
}
