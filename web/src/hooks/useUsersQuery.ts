import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch, fetcher } from "service/http";
import {
  Status,
  User,
  UserCreatePayload,
  UsersListResponse,
  UserWritePayload,
} from "types/User";
import { queryKeys } from "utils/queryClient";

export type UsersFilters = {
  search?: string;
  status?: Status;
  // Sudo only: show just this reseller's (admin's) configs.
  admin?: string;
  sort?: string;
  limit?: number;
  offset?: number;
};

// Drops falsy values so e.g. an empty search string doesn't become
// `?search=` on the wire - mirrors the old dashboard's DashboardContext
// fetchUsers, which deleted any falsy key from the query object before
// sending it.
const cleanFilters = (filters: UsersFilters): Record<string, unknown> => {
  const out: Record<string, unknown> = {};
  (Object.keys(filters) as (keyof UsersFilters)[]).forEach((key) => {
    const value = filters[key];
    if (value) out[key] = value;
  });
  return out;
};

export const useUsersQuery = (filters: UsersFilters) =>
  useQuery({
    queryKey: queryKeys.users(cleanFilters(filters)),
    queryFn: () =>
      fetch<UsersListResponse>("/users", { query: cleanFilters(filters) }),
    placeholderData: (previous) => previous,
  });

const invalidateUsers = (queryClient: ReturnType<typeof useQueryClient>) => {
  queryClient.invalidateQueries({ queryKey: ["users"] });
  queryClient.invalidateQueries({ queryKey: queryKeys.systemStats });
};

export const useCreateUserMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: UserCreatePayload) =>
      fetch<User>("/user", { method: "POST", body }),
    onSuccess: () => invalidateUsers(queryClient),
  });
};

export const useUpdateUserMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      username,
      body,
    }: {
      username: string;
      body: UserWritePayload;
    }) => fetch<User>(`/user/${username}`, { method: "PUT", body }),
    onSuccess: () => invalidateUsers(queryClient),
  });
};

export const useDeleteUserMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (username: string) =>
      fetch(`/user/${username}`, { method: "DELETE" }),
    onSuccess: () => invalidateUsers(queryClient),
  });
};

export const useResetUserUsageMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (username: string) =>
      fetch<User>(`/user/${username}/reset`, { method: "POST" }),
    onSuccess: () => invalidateUsers(queryClient),
  });
};

export const useRevokeUserSubMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (username: string) =>
      fetch<User>(`/user/${username}/revoke_sub`, { method: "POST" }),
    onSuccess: () => invalidateUsers(queryClient),
  });
};

/**
 * Saves the user list as a CSV file. The export carries the same filters as the list (search,
 * status, reseller, sort) but no paging. Fetched with the bearer token and handed to the
 * browser through a synthetic download link - a plain <a href> could not send the token.
 */
export const downloadUsersCsv = async (filters: UsersFilters): Promise<void> => {
  const { limit: _limit, offset: _offset, ...rest } = cleanFilters(filters) as Record<string, unknown>;
  const blob = await fetcher<Blob, "blob">("/users/export", { query: rest, responseType: "blob" });
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = url;
    a.download = "users.csv";
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(url);
  }
};
