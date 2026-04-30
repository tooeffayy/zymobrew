import { Link, useNavigate } from "react-router-dom";

import { Recipe, api } from "../api";
import { RecipeForm } from "../components/RecipeForm";

export function RecipeCreate() {
  const navigate = useNavigate();

  return (
    <div className="page">
      <p className="back-link"><Link to="/">← Back to recipes</Link></p>
      <h1>New recipe</h1>

      <RecipeForm
        mode="create"
        submitLabel="Create recipe"
        submittingLabel="Creating…"
        cancelTo="/"
        onSubmit={async (payload) => {
          const res = await api.post<Recipe>("/api/recipes", payload);
          navigate(`/recipes/${res.id}`, { replace: true });
        }}
      />
    </div>
  );
}
