export function Card({ children, className = '' }) {
  return <div className={`card ${className}`}>{children}</div>
}

export function Button({ 
  children, 
  variant = 'primary', 
  onClick, 
  disabled = false,
  className = '',
  type = 'button'
}) {
  const variants = {
    primary: 'btn-primary',
    secondary: 'btn-secondary',
  }
  
  return (
    <button
      type={type}
      onClick={onClick}
      disabled={disabled}
      className={`${variants[variant]} disabled:opacity-50 disabled:cursor-not-allowed ${className}`}
    >
      {children}
    </button>
  )
}

export function Input({
  label,
  name = '',
  type = 'text',
  value,
  onChange,
  placeholder = '',
  required = false,
  className = '',
}) {
  return (
    <div className="mb-4">
      {label && <label className="block text-sm font-medium mb-1">{label}</label>}
      <input
        name={name}
        type={type}
        value={value}
        onChange={onChange}
        placeholder={placeholder}
        required={required}
        className={`input-field w-full ${className}`}
      />
    </div>
  )
}

export function Toast({ message, type = 'info', onClose }) {
  const bgColor = {
    success: 'bg-green-100 text-green-800',
    error: 'bg-red-100 text-red-800',
    info: 'bg-blue-100 text-blue-800',
  }
  
  return (
    <div className={`${bgColor[type]} p-4 rounded-lg mb-4 flex justify-between items-center`}>
      <span>{message}</span>
      <button onClick={onClose} className="text-xl">&times;</button>
    </div>
  )
}

export function Loading() {
  return (
    <div className="flex items-center justify-center p-8">
      <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-green-500"></div>
    </div>
  )
}
