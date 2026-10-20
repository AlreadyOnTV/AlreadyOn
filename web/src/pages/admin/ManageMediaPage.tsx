import LibraryCatalog from "./LibraryCatalog";
import "./AdminShared.css";

export default function ManageMediaPage() {
  return (
    <div className="admin-section manage-media-page">
      <LibraryCatalog view="movies" />
      <LibraryCatalog view="shows" />
    </div>
  );
}
