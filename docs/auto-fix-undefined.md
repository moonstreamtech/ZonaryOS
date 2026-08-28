# Auto-fix for Issue #undefined





```json
{
  "files": [
    {
      "path": "migrations/001_roles_system.up.sql",
      "content": "-- Create roles table\nCREATE TABLE roles (\n    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),\n    name VARCHAR(50) NOT NULL UNIQUE,\n    description TEXT,\n    permissions JSONB NOT NULL DEFAULT '{}',\n    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),\n    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()\n);\n\n-- Create user_roles table\nCREATE TABLE user_roles (\n    user_id UUID REFERENCES auth.users(id) ON DELETE CASCADE,\n    role_id UUID REFERENCES roles(id) ON DELETE CASCADE,\n    assigned_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),\n    assigned_by UUID REFERENCES auth.users(id),\n    PRIMARY KEY (user_id, role_id)\n);\n\n-- Enable Row Level Security\nALTER TABLE roles ENABLE ROW LEVEL SECURITY;\nALTER TABLE user_roles ENABLE ROW LEVEL SECURITY;\n\n-- Create RLS policies for roles table\nCREATE POLICY \"Admin full access to roles\" ON roles\n    FOR ALL USING (\n        