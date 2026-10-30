export const getAuthToken = () => {
  return localStorage.getItem("token");
};

const listeners = new Set<() => void>();

export const setAuthToken = (token: string) => {
  localStorage.setItem("token", token);
  listeners.forEach((listener) => listener());
};

export const removeAuthToken = () => {
  localStorage.removeItem("token");
  listeners.forEach((listener) => listener());
};

/** Hears this tab's own logins and logouts, and another tab's through the storage event. */
export const subscribeAuthToken = (listener: () => void) => {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
};
