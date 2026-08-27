(function (root, factory) {
  var api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.NetCoreLiveListControls = api;
})(typeof window !== "undefined" ? window : typeof globalThis !== "undefined" ? globalThis : this, function () {
  function createState(allowedFilters, initialFilter, filterParam) {
    var filters = Array.isArray(allowedFilters) && allowedFilters.length ? allowedFilters.slice() : [""];
    var first = filters.indexOf(initialFilter) >= 0 ? initialFilter : "";
    return {
      query: "",
      filter: first,
      filterParam: typeof filterParam === "string" ? filterParam : "",
      allowedFilters: filters,
      cursor: "",
      previousCursors: [],
      nextCursor: "",
      hasMore: false
    };
  }

  function applyCriteria(state, query, filter) {
    var nextQuery = String(query || "").trim();
    var nextFilter = state.allowedFilters.indexOf(filter) >= 0 ? filter : "";
    var changed = state.query !== nextQuery || state.filter !== nextFilter;
    if (changed) Object.assign(state, { query: nextQuery, filter: nextFilter, cursor: "", previousCursors: [], nextCursor: "", hasMore: false });
    return changed;
  }

  function requestURL(baseURL, endpoint, state, limit) {
    var url = new URL(endpoint, baseURL);
    var params = url.searchParams;
    params.set("limit", String(Number.isInteger(limit) && limit > 0 ? limit : 25));
    if (state.query) params.set("q", state.query);
    if (state.filter && state.filterParam) params.set(state.filterParam, state.filter);
    if (state.cursor) params.set("cursor", state.cursor);
    return url.toString();
  }

  function nextPage(state) {
    if (typeof state.nextCursor !== "string" || !state.nextCursor) return "";
    state.previousCursors.push(state.cursor);
    state.cursor = state.nextCursor;
    return state.cursor;
  }

  function previousPage(state) {
    if (!state.previousCursors.length) return "";
    state.cursor = state.previousCursors.pop();
    return state.cursor;
  }

  function applyResponseMeta(state, meta) {
    var nextCursor = meta && typeof meta.next_cursor === "string" ? meta.next_cursor : "";
    state.nextCursor = nextCursor;
    state.hasMore = !!(meta && meta.has_more === true && nextCursor);
    return state.hasMore;
  }

  return { createState: createState, applyCriteria: applyCriteria, requestURL: requestURL, nextPage: nextPage, previousPage: previousPage, applyResponseMeta: applyResponseMeta };
});
