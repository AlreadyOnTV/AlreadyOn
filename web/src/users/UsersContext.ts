// Profiles come from the server (GET /api/profiles), so every device sees
// the same set and per-profile preferences follow the profile. Only the
// active profile id stays on the device (see deviceStorage.ts).
//
// The list is refetched every five visible minutes and on window focus so changes on
// another device shows up without a reload. Nothing here holds a PIN:
// checks go to the server and only the result comes back.
import { createContext, useContext } from 'react';
import type { AudienceTier, Profile, ProfileDraft, ProfilePatch, PinCheck } from '../api/client';

export type UserRecord = Profile;
export type UserDraft = ProfileDraft;
export type { AudienceTier };

export const AUDIENCE_TIER_OPTIONS: { value: AudienceTier; label: string }[] = [
  { value: 'young_child', label: 'Kids' },
  { value: 'teen', label: 'Teen' },
  { value: 'adult', label: 'Unrestricted' },
];

export function audienceTierLabel(tier: AudienceTier): string {
  return AUDIENCE_TIER_OPTIONS.find((o) => o.value === tier)?.label ?? tier;
}

export function roleLabel(role: Profile['role']): string {
  return role === 'admin' ? 'Admin' : 'Viewer';
}

interface UsersContextValue {
  users: Profile[];
  currentUser: Profile;
  currentUserId: number;
  isAdmin: boolean;
  setCurrentUserId: (id: number) => void;
  addUser: (draft: ProfileDraft) => Promise<Profile>;
  // Applies the change on screen right away, then saves it. On failure the
  // list is reloaded from the server and the error is rethrown.
  updateUser: (id: number, patch: ProfilePatch) => Promise<void>;
  deleteUser: (id: number) => Promise<void>;
  setPin: (id: number, pin: string) => Promise<void>;
  clearPin: (id: number) => Promise<void>;
  verifyPin: (id: number, pin: string) => Promise<PinCheck>;
  uploadIcon: (id: number, image: Blob) => Promise<Profile>;
}

export const UsersContext = createContext<UsersContextValue | null>(null);

export function useUsers() {
  const ctx = useContext(UsersContext);
  if (!ctx) throw new Error('useUsers must be used within a UsersProvider');
  return ctx;
}
