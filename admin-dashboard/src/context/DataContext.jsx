import { createContext } from 'react'

export const DataContext = createContext({
  groupData: null,
  setGroupData: () => {},
  members: [],
  setMembers: () => {},
  ledger: [],
  setLedger: () => {},
})
